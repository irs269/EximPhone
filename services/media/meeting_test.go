package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func meetToken(user int64, meeting string) string {
	s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, meetingClaims{MeetingID: meeting, RegisteredClaims: jwt.RegisteredClaims{
		Subject: strconv.FormatInt(user, 10), Audience: jwt.ClaimStrings{meetingToken}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}}).SignedString(testSecret)
	return s
}

// mclient is a participant the way a browser behaves: two peer connections driven over one WebSocket.
type mclient struct {
	t    *testing.T
	user int64
	ws   *websocket.Conn
	pub  *webrtc.PeerConnection
	sub  *webrtc.PeerConnection

	mu      sync.Mutex
	packets map[string]int // "<stream>|<kind>" -> packets received
	sources map[string]string
	events  chan sigMsg
	answers chan string
	stop    chan struct{}
}

func wsURL(srv *httptest.Server, meeting string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/meetings/" + meeting + "/ws"
}

func joinMeeting(t *testing.T, srv *httptest.Server, meeting string, user int64) *mclient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsURL(srv, meeting), nil)
	if err != nil {
		t.Fatal(err)
	}
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	pub, _ := api.NewPeerConnection(webrtc.Configuration{})
	sub, _ := api.NewPeerConnection(webrtc.Configuration{})
	c := &mclient{t: t, user: user, ws: ws, pub: pub, sub: sub, packets: map[string]int{}, sources: map[string]string{},
		events: make(chan sigMsg, 64), answers: make(chan string, 4), stop: make(chan struct{})}
	t.Cleanup(c.close)

	sub.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		key := tr.StreamID() + "|" + tr.Kind().String()
		for {
			if _, _, err := tr.ReadRTP(); err != nil {
				return
			}
			c.mu.Lock()
			c.packets[key]++
			c.mu.Unlock()
		}
	})
	c.write(sigMsg{Type: "join", Token: meetToken(user, meeting)})
	go c.readLoop()
	return c
}

func (c *mclient) write(m sigMsg) {
	b, _ := json.Marshal(m)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.ws.Write(ctx, websocket.MessageText, b)
}

func (c *mclient) readLoop() {
	for {
		_, data, err := c.ws.Read(context.Background())
		if err != nil {
			close(c.events)
			return
		}
		var m sigMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "sub_offer": // the server offers what the others publish: answer it (non-trickle)
			if err := c.sub.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}); err != nil {
				c.t.Errorf("user %d: sub offer: %v", c.user, err)
				continue
			}
			ans, _ := c.sub.CreateAnswer(nil)
			gathered := webrtc.GatheringCompletePromise(c.sub)
			_ = c.sub.SetLocalDescription(ans)
			select {
			case <-gathered:
			case <-time.After(3 * time.Second):
			}
			c.write(sigMsg{Type: "sub_answer", SDP: c.sub.LocalDescription().SDP})
		case "publish_answer":
			c.answers <- m.SDP
		default:
			select {
			case c.events <- m:
			default:
			}
		}
	}
}

func (c *mclient) expect(typ string) sigMsg {
	c.t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case m, ok := <-c.events:
			if !ok {
				c.t.Fatalf("user %d: connection closed while waiting for %q", c.user, typ)
			}
			if m.Type == typ {
				return m
			}
		case <-deadline:
			c.t.Fatalf("user %d: no %q message", c.user, typ)
		}
	}
}

// publish adds a track (microphone, camera or screen) and renegotiates the publish connection.
func (c *mclient) publish(stream, source string, kind webrtc.RTPCodecType) {
	c.t.Helper()
	capab, pt := opusCap, uint8(111)
	if kind == webrtc.RTPCodecTypeVideo {
		capab, pt = webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, 96
	}
	tr, err := webrtc.NewTrackLocalStaticRTP(capab, fmt.Sprintf("%s-%s", stream, kind), stream)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.pub.AddTransceiverFromTrack(tr, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly}); err != nil {
		c.t.Fatal(err)
	}
	c.sources[stream] = source
	offer, _ := c.pub.CreateOffer(nil)
	gathered := webrtc.GatheringCompletePromise(c.pub)
	if err := c.pub.SetLocalDescription(offer); err != nil {
		c.t.Fatal(err)
	}
	<-gathered
	c.write(sigMsg{Type: "publish_offer", SDP: c.pub.LocalDescription().SDP, Sources: c.sources})
	select {
	case sdp := <-c.answers:
		if err := c.pub.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
			c.t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		c.t.Fatal("no publish answer")
	}
	go func() { // synthetic media: the SFU forwards RTP without decoding it
		seq, tick := uint16(0), time.NewTicker(20*time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-tick.C:
				seq++
				_ = tr.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: pt, SequenceNumber: seq, Timestamp: uint32(seq) * 960}, Payload: bytes.Repeat([]byte{byte(c.user)}, 40)})
			}
		}
	}()
}

func (c *mclient) got(stream string, kind webrtc.RTPCodecType) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.packets[stream+"|"+kind.String()]
}

func (c *mclient) waitFor(stream string, kind webrtc.RTPCodecType, min int) {
	c.t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if c.got(stream, kind) >= min {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.t.Fatalf("user %d: only %d %s packets from stream %q (wanted %d)", c.user, c.got(stream, kind), kind, stream, min)
}

func (c *mclient) close() {
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	_ = c.ws.CloseNow()
	_ = c.pub.Close()
	_ = c.sub.Close()
}

const (
	audio = webrtc.RTPCodecTypeAudio
	video = webrtc.RTPCodecTypeVideo
)

func (c *mclient) publishCam() {
	c.publish(fmt.Sprintf("cam-%d", c.user), "cam", audio)
	c.publish(fmt.Sprintf("cam-%d", c.user), "cam", video)
}

func TestMeetingForwardsAudioVideoAndScreen(t *testing.T) {
	srv, m := newTestServer(t)
	defer srv.Close()

	a := joinMeeting(t, srv, "m1", 1)
	joined := a.expect("joined")
	if joined.You != 1 || len(joined.Peers) != 0 {
		t.Fatalf("unexpected joined: %+v", joined)
	}
	b := joinMeeting(t, srv, "m1", 2)
	if got := len(b.expect("joined").Peers); got != 1 {
		t.Fatalf("B should see 1 peer, saw %d", got)
	}
	if a.expect("peer_joined").User != 2 {
		t.Fatal("A was not told that B joined")
	}
	a.publishCam()
	b.publishCam()

	// a third participant joins and publishes
	c := joinMeeting(t, srv, "m1", 3)
	c.expect("joined")
	c.publishCam()

	for _, x := range []*mclient{a, b, c} {
		for _, other := range []int64{1, 2, 3} {
			if other == x.user {
				continue
			}
			s := fmt.Sprintf("%d:cam", other)
			x.waitFor(s, audio, 10)
			x.waitFor(s, video, 10)
		}
	}
	t.Log("three participants receive each other's audio and video")

	// nobody receives their own media back
	if a.got("1:cam", audio) != 0 || a.got("1:cam", video) != 0 {
		t.Fatal("a participant received their own media")
	}

	// a late joiner, who publishes nothing, gets everything already published
	d := joinMeeting(t, srv, "m1", 4)
	d.expect("joined")
	for _, other := range []int64{1, 2, 3} {
		s := fmt.Sprintf("%d:cam", other)
		d.waitFor(s, audio, 10)
		d.waitFor(s, video, 10)
	}
	t.Log("a late joiner receives the participants already publishing")

	// screen sharing: a second video track from A, declared as "screen"
	a.publish("screen-1", "screen", video)
	on := true
	a.write(sigMsg{Type: "state", Screen: &on})
	for _, x := range []*mclient{b, c, d} {
		x.waitFor("1:screen", video, 10)
	}
	if s := b.expect("state"); s.User != 1 || s.Screen == nil || !*s.Screen {
		t.Fatalf("B should be told that A shares the screen: %+v", s)
	}
	t.Log("the screen is forwarded as its own stream next to the camera")
	if a.got("1:screen", video) != 0 {
		t.Fatal("the sharer received their own screen")
	}

	room := m.meet.room("m1")
	room.mu.Lock()
	pa := room.parts[1]
	room.mu.Unlock()
	pa.mu.Lock()
	_, hasScreen := pa.published[pa.keyFor("screen", video)]
	pa.mu.Unlock()
	if !hasScreen {
		t.Fatal("the screen track is not registered on the server")
	}
	off := false
	a.write(sigMsg{Type: "state", Screen: &off})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pa.mu.Lock()
		_, hasScreen = pa.published[pa.keyFor("screen", video)]
		pa.mu.Unlock()
		if !hasScreen {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if hasScreen {
		t.Fatal("the screen track was not removed when sharing stopped")
	}
	t.Log("stopping the share removes the screen stream")

	// someone leaves: the others are told, and nothing is left behind once everybody is gone
	c.write(sigMsg{Type: "leave"})
	if left := a.expect("peer_left"); left.User != 3 {
		t.Fatalf("A was told that %d left", left.User)
	}
	for _, x := range []*mclient{a, b, d} {
		x.close()
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && m.meet.count() > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if m.meet.count() != 0 {
		t.Fatalf("%d meeting rooms leaked", m.meet.count())
	}
}

func TestMeetingRejectsBadTokens(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	for name, token := range map[string]string{"garbage": "nope", "other meeting": meetToken(1, "another"), "call token": mediaToken(1, "m2")} {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		ws, _, err := websocket.Dial(ctx, wsURL(srv, "m2"), nil)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(sigMsg{Type: "join", Token: token})
		_ = ws.Write(ctx, websocket.MessageText, b)
		if _, _, err := ws.Read(ctx); err == nil {
			t.Fatalf("%s: the connection stayed open", name)
		} else if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Fatalf("%s: closed with %v", name, err)
		}
		cancel()
	}
}

func TestMeetingIsLimitedToTwelve(t *testing.T) {
	r := &mRoom{id: "full", parts: map[int64]*participant{}}
	for i := int64(1); i <= meetingMaxParticipants; i++ {
		r.parts[i] = &participant{user: i}
	}
	if _, _, err := r.admit(&participant{user: 99}); err != errMeetingFull {
		t.Fatalf("the 13th participant should be refused, got %v", err)
	}
	if _, old, err := r.admit(&participant{user: 3}); err != nil || old == nil {
		t.Fatalf("a participant reconnecting must replace its seat, got old=%v err=%v", old, err)
	}
}
