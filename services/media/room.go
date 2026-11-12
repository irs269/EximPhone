package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/irs269/eximphone/services/pkg/sdpmini"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

var (
	errRoomFull = errors.New("room is full")
	opusCap     = webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}
	pcmuCap     = webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: 8000, Channels: 1}
)

// leg is one participant of a call: a browser (WebRTC) or a SIP phone (plain RTP).
type leg interface {
	key() string
	send(p *rtp.Packet)
	close()
}

// peer is a browser leg. Audio received from it is relayed to the other legs.
type peer struct {
	user int64
	pc   *webrtc.PeerConnection
	out  *webrtc.TrackLocalStaticRTP
}

func (p *peer) key() string { return "u" + strconv.FormatInt(p.user, 10) }
func (p *peer) close()      { _ = p.pc.Close() }
func (p *peer) send(pkt *rtp.Packet) {
	if pkt.PayloadType == sdpmini.PTDTMF {
		return // browsers do not negotiate telephone-event in this setup
	}
	_ = p.out.WriteRTP(pkt) // payload type and SSRC are rewritten per destination
}

// room holds the (at most two) legs of one call. Relaying through the server instead of
// peer-to-peer is what allows SIP bridging now and recording later.
type room struct {
	mu   sync.Mutex
	legs map[string]leg
}

func (r *room) others(from string) []leg {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []leg
	for k, l := range r.legs {
		if k != from {
			out = append(out, l)
		}
	}
	return out
}

func (r *room) forward(from string, pkt *rtp.Packet) {
	for _, l := range r.others(from) {
		l.send(pkt)
	}
}

// hasSIP reports whether a phone is already in the call: the browser then negotiates G.711
// (what phones speak) instead of Opus, so the audio can be bridged without transcoding.
func (r *room) hasSIP() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.legs {
		if _, ok := l.(*sipLeg); ok {
			return true
		}
	}
	return false
}

type manager struct {
	opusAPI, g711API *webrtc.API
	log              *slog.Logger
	publicIP         string
	sipMin, sipMax   int

	mu    sync.Mutex
	rooms map[string]*room
	legs  map[string]*sipLeg // by leg id, includes legs that are not active yet
}

func newAPI(se webrtc.SettingEngine, codec webrtc.RTPCodecParameters) (*webrtc.API, error) {
	me := &webrtc.MediaEngine{}
	if err := me.RegisterCodec(codec, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	return webrtc.NewAPI(webrtc.WithSettingEngine(se), webrtc.WithMediaEngine(me)), nil
}

// newManager splits [minPort, maxPort]: the lower half serves WebRTC legs, the upper half SIP legs.
func newManager(publicIP string, minPort, maxPort uint16, log *slog.Logger) (*manager, error) {
	mid := int(minPort) + (int(maxPort)-int(minPort))/2
	se := webrtc.SettingEngine{}
	if err := se.SetEphemeralUDPPortRange(minPort, uint16(mid)); err != nil {
		return nil, err
	}
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	if publicIP != "" {
		// Docker/NAT: advertise the address clients can actually reach.
		se.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)
	}
	opusAPI, err := newAPI(se, webrtc.RTPCodecParameters{RTPCodecCapability: opusCap, PayloadType: 111})
	if err != nil {
		return nil, err
	}
	g711API, err := newAPI(se, webrtc.RTPCodecParameters{RTPCodecCapability: pcmuCap, PayloadType: 0})
	if err != nil {
		return nil, err
	}
	return &manager{
		opusAPI: opusAPI, g711API: g711API, log: log, publicIP: publicIP,
		sipMin: mid + 1, sipMax: int(maxPort),
		rooms: map[string]*room{}, legs: map[string]*sipLeg{},
	}, nil
}

func (m *manager) room(id string) *room {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[id]
	if r == nil {
		r = &room{legs: map[string]leg{}}
		m.rooms[id] = r
	}
	return r
}

func (m *manager) roomCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rooms)
}

// leave removes l from the call and drops the room once empty.
func (m *manager) leave(callID string, l leg) {
	m.mu.Lock()
	r := m.rooms[callID]
	m.mu.Unlock()
	if r == nil {
		l.close()
		return
	}
	r.mu.Lock()
	if r.legs[l.key()] == l {
		delete(r.legs, l.key())
	}
	empty := len(r.legs) == 0
	r.mu.Unlock()
	l.close()
	if empty {
		m.mu.Lock()
		if m.rooms[callID] == r {
			delete(m.rooms, callID)
		}
		m.mu.Unlock()
	}
}

// closeRoom tears down every leg of a call (called by the api/sip when the call ends).
func (m *manager) closeRoom(callID string) {
	m.mu.Lock()
	r := m.rooms[callID]
	delete(m.rooms, callID)
	var pending []*sipLeg
	for id, l := range m.legs {
		if l.callID == callID {
			pending = append(pending, l)
			delete(m.legs, id)
		}
	}
	m.mu.Unlock()
	for _, l := range pending {
		l.close()
	}
	if r == nil {
		return
	}
	r.mu.Lock()
	legs := r.legs
	r.legs = map[string]leg{}
	r.mu.Unlock()
	for _, l := range legs {
		l.close()
	}
}

// join negotiates a browser leg for `user` and returns the SDP answer (non-trickle: candidates are included).
func (m *manager) join(ctx context.Context, callID string, user int64, offer string) (string, error) {
	r := m.room(callID)

	api, capability := m.opusAPI, opusCap
	if r.hasSIP() {
		api, capability = m.g711API, pcmuCap
	}
	out, err := webrtc.NewTrackLocalStaticRTP(capability, "audio", "eximphone")
	if err != nil {
		return "", err
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return "", err
	}
	if _, err := pc.AddTrack(out); err != nil {
		_ = pc.Close()
		return "", err
	}
	p := &peer{user: user, pc: pc, out: out}

	r.mu.Lock()
	old := r.legs[p.key()]
	if old == nil && len(r.legs) >= 2 {
		r.mu.Unlock()
		_ = pc.Close()
		return "", errRoomFull
	}
	r.legs[p.key()] = p // a re-join (reconnect, transfer) replaces the previous leg
	r.mu.Unlock()
	if old != nil {
		old.close()
	}

	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if remote.Kind() != webrtc.RTPCodecTypeAudio {
			return
		}
		for {
			pkt, _, err := remote.ReadRTP()
			if err != nil {
				return
			}
			r.forward(p.key(), pkt)
		}
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		m.log.Debug("peer state", "call", callID, "user", user, "state", s.String())
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			m.leave(callID, p)
		}
	})

	fail := func(err error) (string, error) { m.leave(callID, p); return "", err }
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return fail(fmt.Errorf("bad offer: %w", err))
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return fail(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return fail(err)
	}
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	return pc.LocalDescription().SDP, nil
}
