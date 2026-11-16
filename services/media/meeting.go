package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// Meetings: a selective forwarding unit (SFU) for several participants with audio, camera and screen sharing.
//
// Every participant opens two WebRTC connections to this service, driven over one WebSocket:
//
//	publish  (client -> server): the browser offers its microphone, camera and screen tracks; it renegotiates by itself
//	         when it starts or stops sharing the screen;
//	subscribe (server -> client): the server offers the tracks of the other participants and renegotiates whenever one
//	         arrives or leaves.
//
// Two connections avoid offer collisions (each side only ever makes offers on its own connection). Each published
// track is read once and its RTP packets are copied to a local track per subscriber; the server never decodes media.
// Keyframes are requested from the publisher when a subscriber joins and whenever a subscriber reports packet loss.
// ICE is non-trickle (candidates are inside the SDP), like the one-to-one calls.

const (
	meetingMaxParticipants = 12
	meetingToken           = "meeting" // JWT audience of the tokens issued by the api
)

type meetingClaims struct {
	MeetingID string `json:"mid"`
	jwt.RegisteredClaims
}

func newMeetAPI(se webrtc.SettingEngine) (*webrtc.API, error) {
	me := &webrtc.MediaEngine{}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: opusCap, PayloadType: 111}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	vp8 := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
		RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "ccm", Parameter: "fir"}}}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: vp8, PayloadType: 96}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(me, ir); err != nil { // NACK retransmissions, receiver reports
		return nil, err
	}
	return webrtc.NewAPI(webrtc.WithSettingEngine(se), webrtc.WithMediaEngine(me), webrtc.WithInterceptorRegistry(ir)), nil
}

// ---------- messages ----------

type sigMsg struct {
	Type    string            `json:"type"`
	Token   string            `json:"token,omitempty"`
	SDP     string            `json:"sdp,omitempty"`
	Sources map[string]string `json:"sources,omitempty"` // MediaStream id -> "cam" | "screen" (publish_offer)
	Mic     *bool             `json:"mic,omitempty"`
	Cam     *bool             `json:"cam,omitempty"`
	Screen  *bool             `json:"screen,omitempty"`
	User    int64             `json:"user,omitempty"`
	You     int64             `json:"you,omitempty"`
	Peers   []peerInfo        `json:"peers,omitempty"`
	Error   string            `json:"error,omitempty"`
}

type peerInfo struct {
	User   int64 `json:"user"`
	Mic    bool  `json:"mic"`
	Cam    bool  `json:"cam"`
	Screen bool  `json:"screen"`
}

// ---------- room ----------

type subTrack struct {
	local  *webrtc.TrackLocalStaticRTP
	sender *webrtc.RTPSender
}

// pubTrack is one track published by a participant, fanned out to every other participant.
type pubTrack struct {
	key    string // "<user>:<source>:<kind>"
	owner  *participant
	source string
	kind   webrtc.RTPCodecType
	remote *webrtc.TrackRemote
	mu     sync.Mutex
	subs   map[int64]*subTrack
}

type participant struct {
	user int64
	room *mRoom
	ws   *websocket.Conn
	pub  *webrtc.PeerConnection
	sub  *webrtc.PeerConnection

	mu        sync.Mutex
	sources   map[string]string
	published map[string]*pubTrack
	mic, cam  bool
	screen    bool

	negMu       sync.Mutex
	negotiating bool
	pending     bool
	offered     bool // at least one subscribe offer was made
	closed      bool
}

type mRoom struct {
	id    string
	mm    *meetings
	mu    sync.Mutex
	parts map[int64]*participant
}

type meetings struct {
	m     *manager
	mu    sync.Mutex
	rooms map[string]*mRoom
}

func newMeetings(m *manager) *meetings { return &meetings{m: m, rooms: map[string]*mRoom{}} }

func (mm *meetings) room(id string) *mRoom {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	r := mm.rooms[id]
	if r == nil {
		r = &mRoom{id: id, mm: mm, parts: map[int64]*participant{}}
		mm.rooms[id] = r
	}
	return r
}

func (mm *meetings) count() int {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	return len(mm.rooms)
}

func (p *participant) send(msg sigMsg) {
	b, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.ws.Write(ctx, websocket.MessageText, b)
}

func (r *mRoom) others(except int64) []*participant {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*participant, 0, len(r.parts))
	for u, p := range r.parts {
		if u != except {
			out = append(out, p)
		}
	}
	return out
}

func (r *mRoom) broadcast(except int64, msg sigMsg) {
	for _, o := range r.others(except) {
		o.send(msg)
	}
}

// ---------- publishing ----------

// handlePublishOffer answers the browser's offer on the publish connection (first offer and every renegotiation).
func (p *participant) handlePublishOffer(ctx context.Context, msg sigMsg) error {
	p.mu.Lock()
	for id, src := range msg.Sources {
		if src == "cam" || src == "screen" {
			p.sources[id] = src
		}
	}
	p.mu.Unlock()
	if err := p.pub.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: msg.SDP}); err != nil {
		return fmt.Errorf("bad offer: %w", err)
	}
	answer, err := p.pub.CreateAnswer(nil)
	if err != nil {
		return err
	}
	gathered := webrtc.GatheringCompletePromise(p.pub)
	if err := p.pub.SetLocalDescription(answer); err != nil {
		return err
	}
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}
	p.send(sigMsg{Type: "publish_answer", SDP: p.pub.LocalDescription().SDP})
	return nil
}

func (p *participant) sourceOf(remote *webrtc.TrackRemote) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sources[remote.StreamID()]; ok {
		return s
	}
	if remote.Kind() == webrtc.RTPCodecTypeVideo { // no declaration: the first video is the camera, any other is the screen
		if _, cam := p.published[p.keyFor("cam", webrtc.RTPCodecTypeVideo)]; cam {
			return "screen"
		}
	}
	return "cam"
}

func (p *participant) keyFor(source string, kind webrtc.RTPCodecType) string {
	return fmt.Sprintf("%d:%s:%s", p.user, source, kind.String())
}

func (p *participant) onTrack(remote *webrtc.TrackRemote) {
	source := p.sourceOf(remote)
	if remote.Kind() == webrtc.RTPCodecTypeAudio {
		source = "cam" // the microphone always belongs to the person, not to the screen
	}
	pt := &pubTrack{key: p.keyFor(source, remote.Kind()), owner: p, source: source, kind: remote.Kind(), remote: remote, subs: map[int64]*subTrack{}}

	p.mu.Lock()
	old := p.published[pt.key]
	p.published[pt.key] = pt
	p.mu.Unlock()
	if old != nil {
		p.room.unsubscribeAll(old)
	}
	for _, o := range p.room.others(p.user) {
		p.room.subscribe(o, pt)
	}
	// fan out until the track ends
	go func() {
		for {
			pkt, _, err := remote.ReadRTP()
			if err != nil {
				break
			}
			pt.mu.Lock()
			for _, st := range pt.subs {
				_ = st.local.WriteRTP(pkt)
			}
			pt.mu.Unlock()
		}
		p.unpublish(pt)
	}()
}

func (p *participant) unpublish(pt *pubTrack) {
	p.mu.Lock()
	if p.published[pt.key] == pt {
		delete(p.published, pt.key)
	}
	p.mu.Unlock()
	p.room.unsubscribeAll(pt)
}

func (p *participant) requestKeyframe(ssrc webrtc.SSRC) {
	if p.pub != nil {
		_ = p.pub.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(ssrc)}})
	}
}

// ---------- subscribing ----------

// subscribe forwards pt to participant `to` and renegotiates its subscribe connection.
func (r *mRoom) subscribe(to *participant, pt *pubTrack) {
	to.negMu.Lock()
	if to.closed {
		to.negMu.Unlock()
		return
	}
	to.negMu.Unlock()
	streamID := fmt.Sprintf("%d:%s", pt.owner.user, pt.source)
	local, err := webrtc.NewTrackLocalStaticRTP(pt.remote.Codec().RTPCodecCapability, pt.key, streamID)
	if err != nil {
		return
	}
	sender, err := to.sub.AddTrack(local)
	if err != nil {
		return
	}
	pt.mu.Lock()
	pt.subs[to.user] = &subTrack{local: local, sender: sender}
	pt.mu.Unlock()
	go func() { // what the subscriber reports (picture loss) goes back to the publisher
		for {
			pkts, _, err := sender.ReadRTCP()
			if err != nil {
				return
			}
			for _, pkt := range pkts {
				switch pkt.(type) {
				case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
					pt.owner.requestKeyframe(pt.remote.SSRC())
				}
			}
		}
	}()
	if pt.kind == webrtc.RTPCodecTypeVideo { // the new subscriber needs a keyframe to start decoding
		go func() {
			for _, d := range []time.Duration{0, 700 * time.Millisecond, 2 * time.Second, 5 * time.Second} {
				time.Sleep(d)
				pt.owner.requestKeyframe(pt.remote.SSRC())
			}
		}()
	}
	to.negotiate()
}

func (r *mRoom) unsubscribeAll(pt *pubTrack) {
	pt.mu.Lock()
	subs := pt.subs
	pt.subs = map[int64]*subTrack{}
	pt.mu.Unlock()
	r.mu.Lock()
	targets := make(map[int64]*participant, len(subs))
	for u := range subs {
		targets[u] = r.parts[u]
	}
	r.mu.Unlock()
	for u, st := range subs {
		if to := targets[u]; to != nil {
			_ = to.sub.RemoveTrack(st.sender)
			to.negotiate()
		}
	}
}

// negotiate sends a new subscribe offer, one at a time (a request made meanwhile is replayed afterwards).
func (p *participant) negotiate() {
	p.negMu.Lock()
	if p.closed {
		p.negMu.Unlock()
		return
	}
	if p.negotiating {
		p.pending = true
		p.negMu.Unlock()
		return
	}
	if !p.offered && len(p.sub.GetSenders()) == 0 {
		p.negMu.Unlock()
		return // nothing to offer yet
	}
	p.negotiating = true
	p.negMu.Unlock()
	go func() {
		offer, err := p.sub.CreateOffer(nil)
		if err == nil {
			gathered := webrtc.GatheringCompletePromise(p.sub)
			if err = p.sub.SetLocalDescription(offer); err == nil {
				select {
				case <-gathered:
				case <-time.After(3 * time.Second):
				}
				p.negMu.Lock()
				p.offered = true
				p.negMu.Unlock()
				p.send(sigMsg{Type: "sub_offer", SDP: p.sub.LocalDescription().SDP})
				return
			}
		}
		p.negMu.Lock()
		p.negotiating = false
		p.negMu.Unlock()
	}()
}

func (p *participant) handleSubAnswer(sdp string) error {
	err := p.sub.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
	p.negMu.Lock()
	p.negotiating = false
	again := p.pending
	p.pending = false
	p.negMu.Unlock()
	if again {
		p.negotiate()
	}
	return err
}

// ---------- lifecycle ----------

func (mm *meetings) newParticipant(r *mRoom, user int64, ws *websocket.Conn) (*participant, error) {
	pub, err := mm.m.meetAPI.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	sub, err := mm.m.meetAPI.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		_ = pub.Close()
		return nil, err
	}
	p := &participant{user: user, room: r, ws: ws, pub: pub, sub: sub, sources: map[string]string{}, published: map[string]*pubTrack{}, mic: true, cam: false}
	pub.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { p.onTrack(remote) })
	return p, nil
}

var errMeetingFull = errors.New("meeting is full")

// admit adds p to the room (a second connection by the same user replaces the first one) and subscribes it to what is published.
func (r *mRoom) admit(p *participant) ([]peerInfo, *participant, error) {
	r.mu.Lock()
	old := r.parts[p.user]
	if old == nil && len(r.parts) >= meetingMaxParticipants {
		r.mu.Unlock()
		return nil, nil, errMeetingFull
	}
	r.parts[p.user] = p
	peers := make([]peerInfo, 0, len(r.parts))
	for u, o := range r.parts {
		if u != p.user {
			o.mu.Lock()
			peers = append(peers, peerInfo{User: u, Mic: o.mic, Cam: o.cam, Screen: o.screen})
			o.mu.Unlock()
		}
	}
	r.mu.Unlock()
	return peers, old, nil
}

func (r *mRoom) initialSubscribe(p *participant) {
	for _, o := range r.others(p.user) {
		o.mu.Lock()
		tracks := make([]*pubTrack, 0, len(o.published))
		for _, pt := range o.published {
			tracks = append(tracks, pt)
		}
		o.mu.Unlock()
		for _, pt := range tracks {
			r.subscribe(p, pt)
		}
	}
}

// remove drops p from the room; it returns true when the room is now empty.
func (mm *meetings) remove(r *mRoom, p *participant) {
	p.negMu.Lock()
	p.closed = true
	p.negMu.Unlock()
	r.mu.Lock()
	replaced := r.parts[p.user] != p // a newer connection of the same user took the seat
	if !replaced {
		delete(r.parts, p.user)
	}
	empty := len(r.parts) == 0
	r.mu.Unlock()

	p.mu.Lock()
	tracks := make([]*pubTrack, 0, len(p.published))
	for _, pt := range p.published {
		tracks = append(tracks, pt)
	}
	p.published = map[string]*pubTrack{}
	p.mu.Unlock()
	for _, pt := range tracks {
		r.unsubscribeAll(pt)
	}
	// stop receiving what the others send to p
	r.mu.Lock()
	others := make([]*participant, 0, len(r.parts))
	for _, o := range r.parts {
		others = append(others, o)
	}
	r.mu.Unlock()
	for _, o := range others {
		o.mu.Lock()
		for _, pt := range o.published {
			pt.mu.Lock()
			delete(pt.subs, p.user)
			pt.mu.Unlock()
		}
		o.mu.Unlock()
	}
	_ = p.pub.Close()
	_ = p.sub.Close()
	if !replaced {
		r.broadcast(p.user, sigMsg{Type: "peer_left", User: p.user})
		mm.m.notifyMeeting(r.id, p.user, "left")
	}
	if empty {
		mm.mu.Lock()
		if mm.rooms[r.id] == r {
			delete(mm.rooms, r.id)
		}
		mm.mu.Unlock()
	}
}

// end closes every connection of a meeting (the host ended it).
func (mm *meetings) end(id string) {
	mm.mu.Lock()
	r := mm.rooms[id]
	mm.mu.Unlock()
	if r == nil {
		return
	}
	for _, p := range r.others(0) {
		p.send(sigMsg{Type: "ended"})
		_ = p.ws.Close(websocket.StatusNormalClosure, "meeting ended")
	}
}

func (mm *meetings) serve(jwtSecret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		ws, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ws.SetReadLimit(256 << 10)
		ctx := req.Context()

		// first frame: {"type":"join","token":...}, the short-lived token issued by the api for this user and this meeting
		authCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, data, err := ws.Read(authCtx)
		cancel()
		var first sigMsg
		var claims meetingClaims
		if err != nil || json.Unmarshal(data, &first) != nil || first.Type != "join" {
			_ = ws.Close(websocket.StatusPolicyViolation, "join required")
			return
		}
		tok, err := jwt.ParseWithClaims(first.Token, &claims, func(*jwt.Token) (any, error) { return jwtSecret, nil },
			jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(meetingToken))
		user, perr := strconv.ParseInt(claims.Subject, 10, 64)
		if err != nil || !tok.Valid || perr != nil || claims.MeetingID != id {
			_ = ws.Close(websocket.StatusPolicyViolation, "unauthorized")
			return
		}

		r := mm.room(id)
		p, err := mm.newParticipant(r, user, ws)
		if err != nil {
			_ = ws.Close(websocket.StatusInternalError, "media error")
			return
		}
		peers, old, err := r.admit(p)
		if err != nil {
			p.send(sigMsg{Type: "error", Error: "meeting is full"})
			_ = p.pub.Close()
			_ = p.sub.Close()
			_ = ws.Close(websocket.StatusTryAgainLater, "full")
			return
		}
		if old != nil { // same user, new connection (page reload): the previous one is dropped
			_ = old.ws.Close(websocket.StatusNormalClosure, "replaced")
		}
		defer mm.remove(r, p)
		p.send(sigMsg{Type: "joined", You: user, Peers: peers})
		r.broadcast(user, sigMsg{Type: "peer_joined", User: user})
		r.initialSubscribe(p)

		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var msg sigMsg
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			switch msg.Type {
			case "publish_offer":
				if err := p.handlePublishOffer(ctx, msg); err != nil {
					mm.m.log.Warn("meeting publish failed", "meeting", id, "user", user, "err", err)
					p.send(sigMsg{Type: "error", Error: "negotiation failed"})
				}
			case "sub_answer":
				if err := p.handleSubAnswer(msg.SDP); err != nil {
					mm.m.log.Warn("meeting subscribe failed", "meeting", id, "user", user, "err", err)
				}
			case "state":
				p.mu.Lock()
				if msg.Mic != nil {
					p.mic = *msg.Mic
				}
				if msg.Cam != nil {
					p.cam = *msg.Cam
				}
				screenOff := msg.Screen != nil && !*msg.Screen && p.screen
				if msg.Screen != nil {
					p.screen = *msg.Screen
				}
				state := sigMsg{Type: "state", User: user, Mic: boolp(p.mic), Cam: boolp(p.cam), Screen: boolp(p.screen)}
				var screenTracks []*pubTrack
				if screenOff { // stopping the share removes its track right away, without waiting for the renegotiation
					for k, pt := range p.published {
						if pt.source == "screen" {
							screenTracks = append(screenTracks, pt)
							delete(p.published, k)
						}
					}
				}
				p.mu.Unlock()
				for _, pt := range screenTracks {
					r.unsubscribeAll(pt)
				}
				r.broadcast(user, state)
			case "leave":
				return
			}
		}
	}
}

func boolp(b bool) *bool { return &b }

// notifyMeeting tells the api that a participant left (best effort).
func (m *manager) notifyMeeting(meetingID string, user int64, what string) {
	base := os.Getenv("API_URL")
	if base == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"user_id": user})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/internal/meetings/"+meetingID+"/"+what, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", m.internalToken)
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
	}()
}
