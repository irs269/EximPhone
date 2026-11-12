package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

var (
	errRoomFull = errors.New("room is full")
	opusCap     = webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}
)

// peer is one participant's WebRTC leg. Audio received from it is relayed to the other leg's `out` track.
type peer struct {
	user int64
	pc   *webrtc.PeerConnection
	out  *webrtc.TrackLocalStaticRTP
}

// room holds the (at most two) legs of one call. Relaying through the server instead of
// peer-to-peer is what allows recording, SIP bridging and transcoding in later phases.
type room struct {
	mu    sync.Mutex
	peers map[int64]*peer
}

func (r *room) other(user int64) *peer {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, p := range r.peers {
		if id != user {
			return p
		}
	}
	return nil
}

type manager struct {
	api   *webrtc.API
	log   *slog.Logger
	mu    sync.Mutex
	rooms map[string]*room
}

func newManager(publicIP string, minPort, maxPort uint16, log *slog.Logger) (*manager, error) {
	se := webrtc.SettingEngine{}
	if err := se.SetEphemeralUDPPortRange(minPort, maxPort); err != nil {
		return nil, err
	}
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	if publicIP != "" {
		// Docker/NAT: advertise the address clients can actually reach.
		se.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)
	}
	me := &webrtc.MediaEngine{}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: opusCap, PayloadType: 111}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	return &manager{
		api:   webrtc.NewAPI(webrtc.WithSettingEngine(se), webrtc.WithMediaEngine(me)),
		log:   log,
		rooms: map[string]*room{},
	}, nil
}

func (m *manager) room(id string) *room {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[id]
	if r == nil {
		r = &room{peers: map[int64]*peer{}}
		m.rooms[id] = r
	}
	return r
}

func (m *manager) roomCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rooms)
}

// leave removes p from the call and drops the room once empty.
func (m *manager) leave(callID string, p *peer) {
	m.mu.Lock()
	r := m.rooms[callID]
	m.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.peers[p.user] == p {
		delete(r.peers, p.user)
	}
	empty := len(r.peers) == 0
	r.mu.Unlock()
	_ = p.pc.Close()
	if empty {
		m.mu.Lock()
		if m.rooms[callID] == r {
			delete(m.rooms, callID)
		}
		m.mu.Unlock()
	}
}

// closeRoom tears down every leg of a call (called by the api when the call ends).
func (m *manager) closeRoom(callID string) {
	m.mu.Lock()
	r := m.rooms[callID]
	delete(m.rooms, callID)
	m.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	peers := r.peers
	r.peers = map[int64]*peer{}
	r.mu.Unlock()
	for _, p := range peers {
		_ = p.pc.Close()
	}
}

// join negotiates a leg for `user` and returns the SDP answer (non-trickle: candidates are included).
func (m *manager) join(ctx context.Context, callID string, user int64, offer string) (string, error) {
	r := m.room(callID)

	out, err := webrtc.NewTrackLocalStaticRTP(opusCap, "audio", "eximphone")
	if err != nil {
		return "", err
	}
	pc, err := m.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return "", err
	}
	if _, err := pc.AddTrack(out); err != nil {
		_ = pc.Close()
		return "", err
	}
	p := &peer{user: user, pc: pc, out: out}

	r.mu.Lock()
	old := r.peers[user]
	if old == nil && len(r.peers) >= 2 {
		r.mu.Unlock()
		_ = pc.Close()
		return "", errRoomFull
	}
	r.peers[user] = p // a re-join (reconnect, transfer) replaces the previous leg
	r.mu.Unlock()
	if old != nil {
		_ = old.pc.Close()
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
			if o := r.other(user); o != nil {
				_ = o.out.WriteRTP(pkt) // payload type and SSRC are rewritten per destination
			}
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
