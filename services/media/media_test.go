package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

var (
	testSecret = []byte("test-secret-test-secret-test-secret")
	testToken  = "internal-token"
)

func mediaToken(user int64, call string) string {
	s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, mediaClaims{CallID: call, RegisteredClaims: jwt.RegisteredClaims{
		Subject: strconv.FormatInt(user, 10), Audience: jwt.ClaimStrings{"media"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}}).SignedString(testSecret)
	return s
}

func newTestServer(t *testing.T) (*httptest.Server, *manager) {
	t.Helper()
	m, err := newManager("", 41000, 41200, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	routes(m, testSecret, testToken)(r)
	return httptest.NewServer(r), m
}

func post(t *testing.T, srv *httptest.Server, call, token, sdp string) (*http.Response, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"sdp": sdp})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/calls/"+call+"/offer", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct{ SDP string }
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out.SDP
}

type client struct {
	pc  *webrtc.PeerConnection
	out *webrtc.TrackLocalStaticRTP
	got chan []byte
}

// dial negotiates a leg the way the browser does: audio sendrecv, full SDP with candidates.
func dial(t *testing.T, srv *httptest.Server, call string, user int64) *client {
	return dialWith(t, srv, call, user, opusCap, 111)
}

func dialWith(t *testing.T, srv *httptest.Server, call string, user int64, capability webrtc.RTPCodecCapability, pt webrtc.PayloadType) *client {
	t.Helper()
	me := &webrtc.MediaEngine{}
	_ = me.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: capability, PayloadType: pt}, webrtc.RTPCodecTypeAudio)
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(me)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	out, _ := webrtc.NewTrackLocalStaticRTP(capability, "audio", "client")
	if _, err := pc.AddTrack(out); err != nil {
		t.Fatal(err)
	}
	c := &client{pc: pc, out: out, got: make(chan []byte, 64)}
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			p, _, err := tr.ReadRTP()
			if err != nil {
				return
			}
			c.got <- p.Payload
		}
	})
	offer, _ := pc.CreateOffer(nil)
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered
	res, answer := post(t, srv, call, mediaToken(user, call), pc.LocalDescription().SDP)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("offer rejected: %d", res.StatusCode)
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}); err != nil {
		t.Fatal(err)
	}
	return c
}

// stream sends marker packets until stop is closed.
func (c *client) stream(marker byte, stop <-chan struct{}) {
	go func() {
		seq := uint16(0)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				seq++
				_ = c.out.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: seq, Timestamp: uint32(seq) * 960}, Payload: bytes.Repeat([]byte{marker}, 20)})
			}
		}
	}()
}

func expectMarker(t *testing.T, c *client, marker byte) {
	t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case p := <-c.got:
			if len(p) > 0 && p[0] == marker {
				return
			}
		case <-deadline:
			t.Fatalf("audio marker %d never arrived", marker)
		}
	}
}

func TestRelayBetweenTwoLegs(t *testing.T) {
	srv, m := newTestServer(t)
	defer srv.Close()

	a := dial(t, srv, "call-1", 1)
	b := dial(t, srv, "call-1", 2)
	stop := make(chan struct{})
	defer close(stop)
	a.stream(0xA1, stop)
	b.stream(0xB2, stop)

	expectMarker(t, b, 0xA1) // A's audio reaches B
	expectMarker(t, a, 0xB2) // and B's audio reaches A
	if m.roomCount() != 1 {
		t.Fatalf("rooms = %d", m.roomCount())
	}
}

func TestAuthorization(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	if res, _ := post(t, srv, "c", "garbage", "v=0"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad token: %d", res.StatusCode)
	}
	if res, _ := post(t, srv, "c-other", mediaToken(1, "c"), "v=0"); res.StatusCode != http.StatusForbidden {
		t.Errorf("token for another call: %d", res.StatusCode)
	}
	if res, _ := post(t, srv, "c", mediaToken(1, "c"), "not an sdp"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad sdp: %d", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/calls/c", nil)
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("delete without internal token: %d", res.StatusCode)
	}
}

func TestRoomIsLimitedToTwoLegsAndClosedOnDelete(t *testing.T) {
	srv, m := newTestServer(t)
	defer srv.Close()
	dial(t, srv, "full", 1)
	dial(t, srv, "full", 2)

	third := &webrtc.MediaEngine{}
	_ = third.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: opusCap, PayloadType: 111}, webrtc.RTPCodecTypeAudio)
	pc, _ := webrtc.NewAPI(webrtc.WithMediaEngine(third)).NewPeerConnection(webrtc.Configuration{})
	defer pc.Close()
	tr, _ := webrtc.NewTrackLocalStaticRTP(opusCap, "a", "b")
	_, _ = pc.AddTrack(tr)
	offer, _ := pc.CreateOffer(nil)
	_ = pc.SetLocalDescription(offer)
	if res, _ := post(t, srv, "full", mediaToken(3, "full"), offer.SDP); res.StatusCode != http.StatusConflict {
		t.Fatalf("third leg: %d, want 409", res.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/calls/full", nil)
	req.Header.Set("X-Internal-Token", testToken)
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if m.roomCount() != 0 {
		t.Fatalf("room not removed: %d", m.roomCount())
	}
}

func internalPost(t *testing.T, srv *httptest.Server, path string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(b))
	req.Header.Set("X-Internal-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// A desk phone (plain RTP, G.711) talks to a browser: the browser leg must negotiate PCMU and audio must flow both ways.
func TestSIPLegBridgesToBrowserWithG711(t *testing.T) {
	srv, m := newTestServer(t)
	defer srv.Close()

	if code, _ := internalPost(t, srv, "/legs", map[string]string{"call_id": "x"}); code != 200 {
		t.Fatalf("leg allocation: %d", code)
	}
	code, leg := internalPost(t, srv, "/legs", map[string]string{"call_id": "bridge"})
	if code != 200 {
		t.Fatalf("leg allocation: %d", code)
	}
	legPort := int(leg["port"].(float64))
	if legPort < m.sipMin || legPort > m.sipMax {
		t.Fatalf("port %d outside the SIP range %d-%d", legPort, m.sipMin, m.sipMax)
	}
	phone, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()
	if code, _ := internalPost(t, srv, "/legs/"+leg["leg_id"].(string)+"/activate", map[string]string{"remote": phone.LocalAddr().String()}); code != http.StatusNoContent {
		t.Fatalf("activate: %d", code)
	}

	browser := dialWith(t, srv, "bridge", 5, pcmuCap, 0) // the room now has a phone, so media must answer with G.711
	stop := make(chan struct{})
	defer close(stop)
	browser.stream(0xB5, stop)

	// phone -> browser
	go func() {
		seq := uint16(0)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				seq++
				b, _ := (&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: seq, Timestamp: uint32(seq) * 160, SSRC: 777}, Payload: bytes.Repeat([]byte{0xA7}, 160)}).Marshal()
				_, _ = phone.WriteToUDP(b, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: legPort})
			}
		}
	}()
	expectMarker(t, browser, 0xA7)

	// browser -> phone (the leg answers to the phone's real source address: latching)
	buf := make([]byte, 1600)
	deadline := time.Now().Add(8 * time.Second)
	for {
		_ = phone.SetReadDeadline(deadline)
		n, _, err := phone.ReadFromUDP(buf)
		if err != nil {
			t.Fatal("no RTP from the browser reached the phone")
		}
		var p rtp.Packet
		if p.Unmarshal(buf[:n]) == nil && len(p.Payload) > 0 && p.Payload[0] == 0xB5 {
			if p.PayloadType != 0 {
				t.Fatalf("phone received payload type %d, want 0 (PCMU)", p.PayloadType)
			}
			break
		}
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/calls/bridge", nil)
	req.Header.Set("X-Internal-Token", testToken)
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != http.StatusNoContent {
		t.Fatalf("close: %d", res.StatusCode)
	}
	if m.roomCount() != 0 {
		t.Fatalf("room not removed: %d", m.roomCount())
	}
}

func TestSIPLegEndpointsNeedInternalToken(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	res, err := http.Post(srv.URL+"/legs", "application/json", bytes.NewReader([]byte(`{"call_id":"x"}`)))
	if err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %v %v", res, err)
	}
}
