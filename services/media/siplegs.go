package main

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/irs269/eximphone/services/pkg/sdpmini"

	"github.com/pion/rtp"
	"github.com/pion/srtp/v3"
)

var (
	errNoPort = errors.New("no free RTP port")
	errBadKey = errors.New("missing or invalid SRTP key")
)

// sipLeg is a SIP phone's RTP stream (plain RTP over UDP, G.711). It stays "pending" (a reserved port
// advertised in SDP) until the phone answers, then joins the call's room.
type sipLeg struct {
	id, callID string
	uid        int64
	conn       *net.UDPConn
	port       int
	ssrc       uint32
	created    time.Time
	m          *manager

	// SRTP (SDES, AES_CM_128_HMAC_SHA1_80): our own key material goes to the phone in SDP, the phone's key arrives at activation.
	secure  bool
	srtpKey []byte // our send key (16 key bytes + 14 salt bytes)
	cryMu   sync.Mutex
	enc     *srtp.Context
	dec     *srtp.Context

	mu     sync.Mutex
	remote *net.UDPAddr // from SDP at first, then latched to the real source (NAT-friendly "comedia")
	latch  bool
	room   *room
	done   bool
}

func (l *sipLeg) key() string  { return "s" + l.id }
func (l *sipLeg) owner() int64 { return l.uid }

// newSIPLeg reserves a UDP port from the SIP half of the range.
func (m *manager) newSIPLeg(callID string, userID int64, secure bool) (*sipLeg, error) {
	span := int64(m.sipMax - m.sipMin + 1)
	if span <= 0 {
		return nil, errNoPort
	}
	var ssrcB [4]byte
	_, _ = rand.Read(ssrcB[:])
	var idB [8]byte
	_, _ = rand.Read(idB[:])
	for try := 0; try < 50; try++ {
		n, err := rand.Int(rand.Reader, big.NewInt(span))
		if err != nil {
			return nil, err
		}
		port := m.sipMin + int(n.Int64())
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
		if err != nil {
			continue // taken
		}
		l := &sipLeg{
			id: fmt.Sprintf("%x", idB), callID: callID, uid: userID, conn: conn, port: port,
			ssrc: binary.BigEndian.Uint32(ssrcB[:]), created: time.Now(), m: m,
		}
		if secure {
			l.secure = true
			l.srtpKey = make([]byte, sdpmini.SRTPKeyLen)
			_, _ = rand.Read(l.srtpKey)
			ctx, err := srtp.CreateContext(l.srtpKey[:16], l.srtpKey[16:], srtp.ProtectionProfileAes128CmHmacSha1_80)
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
			l.enc = ctx
		}
		m.mu.Lock()
		m.legs[l.id] = l
		m.mu.Unlock()
		return l, nil
	}
	return nil, errNoPort
}

func (m *manager) sipLeg(id string) *sipLeg {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.legs[id]
}

// activate puts the leg in its call's room and starts relaying.
func (l *sipLeg) activate(remote *net.UDPAddr, remoteKey []byte) error {
	if l.secure {
		if len(remoteKey) != sdpmini.SRTPKeyLen {
			return errBadKey
		}
		ctx, err := srtp.CreateContext(remoteKey[:16], remoteKey[16:], srtp.ProtectionProfileAes128CmHmacSha1_80)
		if err != nil {
			return errBadKey
		}
		l.dec = ctx
	}
	r := l.m.room(l.callID)
	r.mu.Lock()
	if len(r.legs) >= 2 {
		r.mu.Unlock()
		return errRoomFull
	}
	r.legs[l.key()] = l
	r.mu.Unlock()

	l.mu.Lock()
	l.remote, l.room = remote, r
	l.mu.Unlock()
	go l.readLoop(r)
	return nil
}

func (l *sipLeg) readLoop(r *room) {
	buf := make([]byte, 1600)
	for {
		n, src, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		l.mu.Lock()
		if !l.latch { // symmetric RTP: answer to wherever the phone really sends from
			l.remote, l.latch = src, true
		} else if !l.remote.IP.Equal(src.IP) || l.remote.Port != src.Port {
			l.mu.Unlock()
			continue // ignore packets from other sources once latched
		}
		l.mu.Unlock()
		raw := buf[:n]
		if l.secure { // unauthenticated or replayed packets are dropped here
			plain, err := l.dec.DecryptRTP(nil, raw, nil)
			if err != nil {
				continue
			}
			raw = plain
		}
		var pkt rtp.Packet
		if err := pkt.Unmarshal(raw); err != nil {
			continue
		}
		r.forward(l, &pkt)
	}
}

func (l *sipLeg) send(p *rtp.Packet) {
	l.mu.Lock()
	remote, done := l.remote, l.done
	l.mu.Unlock()
	if remote == nil || done {
		return
	}
	out := *p
	out.SSRC = l.ssrc
	if out.PayloadType != sdpmini.PTDTMF {
		out.PayloadType = sdpmini.PTPCMU
	}
	b, err := out.Marshal()
	if err != nil {
		return
	}
	if l.secure {
		l.cryMu.Lock()
		b, err = l.enc.EncryptRTP(nil, b, nil)
		l.cryMu.Unlock()
		if err != nil {
			return
		}
	}
	_, _ = l.conn.WriteToUDP(b, remote)
}

func (l *sipLeg) close() {
	l.mu.Lock()
	if l.done {
		l.mu.Unlock()
		return
	}
	l.done = true
	r := l.room
	l.mu.Unlock()
	_ = l.conn.Close()
	l.m.mu.Lock()
	delete(l.m.legs, l.id)
	l.m.mu.Unlock()
	if r != nil {
		r.mu.Lock()
		if r.legs[l.key()] == l {
			delete(r.legs, l.key())
		}
		r.mu.Unlock()
	}
}

// gc releases legs that were reserved but never answered.
func (m *manager) gc(maxAge time.Duration) {
	m.mu.Lock()
	var stale []*sipLeg
	for _, l := range m.legs {
		l.mu.Lock()
		pending := l.room == nil
		l.mu.Unlock()
		if pending && time.Since(l.created) > maxAge {
			stale = append(stale, l)
		}
	}
	m.mu.Unlock()
	for _, l := range stale {
		l.close()
	}
}
