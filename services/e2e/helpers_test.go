//go:build e2e || load

// End-to-end tests of the phone integration against a running stack (make dev):
// a real SIP softphone (sipgo) and a real WebRTC client (pion) play the desk phone and the PC.
package e2e

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irs269/eximphone/services/pkg/sdpmini"
	"github.com/irs269/eximphone/services/pkg/sipauth"

	"github.com/coder/websocket"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/pion/rtp"
	"github.com/pion/srtp/v3"
	"github.com/pion/webrtc/v4"
)

var (
	base    = envOr("E2E_BASE", "https://127.0.0.1")
	sipHost = envOr("E2E_SIP", "127.0.0.1:5060")
	domain  = "exim.local"
	// TLS verification is disabled ONLY for the local self-signed development certificate.
	httpc = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func call(t *testing.T, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := httpc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// ---------------- PC (REST + WebSocket + WebRTC) ----------------

type pcUser struct {
	t        *testing.T
	name     string
	id       int64
	ext      string
	token    string
	events   chan map[string]any
	mu       sync.Mutex
	seen     []map[string]any
	cancelWS context.CancelFunc
}

func login(t *testing.T, username string) *pcUser {
	t.Helper()
	code, out := call(t, "", "POST", "/api/auth/login", map[string]string{"username": username, "password": "Demo-Passw0rd!"})
	if code != 200 {
		t.Fatalf("login %s: %d %v", username, code, out)
	}
	u := out["user"].(map[string]any)
	p := &pcUser{t: t, name: username, id: int64(u["id"].(float64)), ext: fmt.Sprint(u["extension"]), token: out["access_token"].(string), events: make(chan map[string]any, 64)}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancelWS = cancel
	conn, _, err := websocket.Dial(ctx, strings.Replace(base, "https", "wss", 1)+"/api/ws", &websocket.DialOptions{HTTPClient: httpc})
	if err != nil {
		t.Fatalf("ws %s: %v", username, err)
	}
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","token":"`+p.token+`"}`))
	ready := make(chan struct{})
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var ev map[string]any
			if json.Unmarshal(data, &ev) != nil {
				continue
			}
			if ev["type"] == "ready" {
				close(ready)
				continue
			}
			p.mu.Lock()
			p.seen = append(p.seen, ev)
			p.mu.Unlock()
			select {
			case p.events <- ev:
			default:
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatalf("ws %s not ready", username)
	}
	t.Cleanup(func() { cancel(); _ = conn.CloseNow() })
	call(t, p.token, "PUT", "/api/presence", map[string]string{"status": "on"})
	return p
}

// wait returns the first event (new or already seen) matching the predicate.
func (p *pcUser) wait(typ string, match func(map[string]any) bool) map[string]any {
	p.t.Helper()
	deadline := time.After(8 * time.Second)
	check := func() map[string]any {
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, ev := range p.seen {
			if ev["type"] == typ && (match == nil || match(ev["data"].(map[string]any))) {
				return ev["data"].(map[string]any)
			}
		}
		return nil
	}
	for {
		if d := check(); d != nil {
			return d
		}
		select {
		case <-p.events:
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			p.t.Fatalf("%s: timeout waiting for event %q", p.name, typ)
		}
	}
}

// reset forgets the events received so far, so the next wait only sees new ones.
func (p *pcUser) reset() {
	p.mu.Lock()
	p.seen = nil
	p.mu.Unlock()
}

func (p *pcUser) has(typ string, match func(map[string]any) bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ev := range p.seen {
		if ev["type"] == typ && (match == nil || match(ev["data"].(map[string]any))) {
			return true
		}
	}
	return false
}

// pcLeg is the WebRTC side of a PC in a call (G.711 because a phone is in the call).
type pcLeg struct {
	pc  *webrtc.PeerConnection
	out *webrtc.TrackLocalStaticRTP
	got chan []byte
}

func (p *pcUser) joinMedia(callID string) *pcLeg {
	p.t.Helper()
	code, tok := call(p.t, p.token, "GET", "/api/calls/"+callID+"/token", nil)
	if code != 200 {
		p.t.Fatalf("media token: %d %v", code, tok)
	}
	me := &webrtc.MediaEngine{}
	_ = me.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: 8000, Channels: 1}, PayloadType: 0}, webrtc.RTPCodecTypeAudio)
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(me)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		p.t.Fatal(err)
	}
	p.t.Cleanup(func() { _ = pc.Close() })
	out, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: 8000, Channels: 1}, "audio", "pc")
	_, _ = pc.AddTrack(out)
	leg := &pcLeg{pc: pc, out: out, got: make(chan []byte, 256)}
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			pkt, _, err := tr.ReadRTP()
			if err != nil {
				return
			}
			select {
			case leg.got <- pkt.Payload:
			default:
			}
		}
	})
	offer, _ := pc.CreateOffer(nil)
	gathered := webrtc.GatheringCompletePromise(pc)
	_ = pc.SetLocalDescription(offer)
	<-gathered
	b, _ := json.Marshal(map[string]string{"sdp": pc.LocalDescription().SDP})
	req, _ := http.NewRequest("POST", base+"/media/calls/"+callID+"/offer", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+tok["token"].(string))
	res, err := httpc.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	var ans struct{ SDP string }
	_ = json.NewDecoder(res.Body).Decode(&ans)
	if res.StatusCode != 200 {
		p.t.Fatalf("media offer: %d", res.StatusCode)
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans.SDP}); err != nil {
		p.t.Fatal(err)
	}
	return leg
}

func (l *pcLeg) stream(marker byte, stop <-chan struct{}) {
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
				_ = l.out.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: seq, Timestamp: uint32(seq) * 160}, Payload: bytes.Repeat([]byte{marker}, 160)})
			}
		}
	}()
}

func (l *pcLeg) expect(t *testing.T, marker byte) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case p := <-l.got:
			if len(p) > 0 && p[0] == marker {
				return
			}
		case <-deadline:
			t.Fatalf("PC never received audio marker %#x from the phone", marker)
		}
	}
}

// ---------------- SIP softphone ----------------

type incoming struct {
	dlg    *sipgo.DialogServerSession
	offer  sdpmini.Audio
	from   string
	answer chan []byte // SDP to answer with; nil rejects with 486
	done   chan struct{}
}

type softphone struct {
	t        *testing.T
	ext      string
	user     string
	pass     string
	alg      string // preferred digest algorithm: SHA-256 or MD5
	port     int
	rtp      *net.UDPConn
	srv      *sipgo.Server
	client   *sipgo.Client
	dialogS  *sipgo.DialogServerCache
	dialogC  *sipgo.DialogClientCache
	invites  chan *incoming
	mu       sync.Mutex
	cancels  int
	byes     int
	lastAuth string // last challenge header, reused to pre-authenticate INVITEs
	pending  map[string]*incoming

	srtp      bool   // offer and use SRTP (SDES)
	myKey     []byte // our SRTP key material
	sendCtx   *srtp.Context
	recvCtx   *srtp.Context
	tlsMode   bool // register over TLS (port 5061)
	wireClear int  // packets seen on the wire that were NOT protected while SRTP was on
}

func newSoftphone(t *testing.T, ext, user, pass, alg string, port int) *softphone {
	t.Helper()
	ua, err := sipgo.NewUA(sipgo.WithUserAgent("e2e-softphone"), sipgo.WithUserAgenTLSConfig(&tls.Config{InsecureSkipVerify: true})) //nolint:gosec // local dev certificate
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := sipgo.NewServer(ua)
	client, err := sipgo.NewClient(ua, sipgo.WithClientHostname("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	contact := sip.ContactHeader{Address: sip.Uri{Scheme: "sip", User: ext, Host: "127.0.0.1", Port: port}}
	rtpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	p := &softphone{t: t, ext: ext, user: user, pass: pass, alg: alg, port: port, rtp: rtpConn, srv: srv, client: client,
		dialogS: sipgo.NewDialogServerCache(client, contact), dialogC: sipgo.NewDialogClientCache(client, contact), invites: make(chan *incoming, 4), pending: map[string]*incoming{}}

	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		dlg, err := p.dialogS.ReadInvite(req, tx)
		if err != nil {
			return
		}
		defer dlg.Close()
		offer, err := sdpmini.Parse(req.Body())
		if err != nil {
			_ = dlg.Respond(488, "Not Acceptable Here", nil)
			return
		}
		in := &incoming{dlg: dlg, offer: offer, from: req.From().Address.User, answer: make(chan []byte, 1), done: make(chan struct{})}
		p.mu.Lock()
		p.pending[req.CallID().Value()] = in
		p.mu.Unlock()
		p.invites <- in
		_ = dlg.Respond(180, "Ringing", nil)
		select {
		case sdp := <-in.answer:
			if sdp == nil {
				_ = dlg.Respond(486, "Busy Here", nil)
				return
			}
			_ = dlg.RespondSDP(sdp)
			<-dlg.Context().Done()
		case <-dlg.Context().Done(): // CANCEL or failure
		}
		close(in.done)
	})
	srv.OnCancel(func(req *sip.Request, tx sip.ServerTransaction) {
		p.mu.Lock()
		p.cancels++
		p.mu.Unlock()
		_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
		// terminate the pending INVITE transaction with 487
		p.mu.Lock()
		in := p.pending[req.CallID().Value()]
		p.mu.Unlock()
		if in != nil {
			_ = in.dlg.Respond(487, "Request Terminated", nil)
		}
	})
	srv.OnAck(func(req *sip.Request, tx sip.ServerTransaction) { _ = p.dialogS.ReadAck(req, tx) })
	srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		p.mu.Lock()
		p.byes++
		p.mu.Unlock()
		if p.dialogS.ReadBye(req, tx) != nil {
			if p.dialogC.ReadBye(req, tx) != nil {
				_ = tx.Respond(sip.NewResponseFromRequest(req, 481, "Call/Transaction Does Not Exist", nil))
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	go func() {
		_ = srv.ListenAndServe(context.WithValue(ctx, sipgo.ListenReadyCtxKey, sipgo.ListenReadyCtxValue(ready)), "udp", fmt.Sprintf("0.0.0.0:%d", port))
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
	}
	t.Cleanup(func() { cancel(); _ = srv.Close(); _ = rtpConn.Close() })
	return p
}

func (p *softphone) counts() (cancels, byes int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cancels, p.byes
}

func (p *softphone) authHeader(challenge, method, uri string) string {
	params, _ := sipauth.Parse(challenge)
	const nc, cnonce = "00000001", "e2ecnonce"
	resp := sipauth.Response(params["algorithm"], p.user, params["realm"], p.pass, method, uri, params["nonce"], nc, cnonce, "auth")
	return fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s", algorithm=%s, qop=auth, nc=%s, cnonce="%s"`,
		p.user, params["realm"], params["nonce"], uri, resp, params["algorithm"], nc, cnonce)
}

// pick returns the challenge for the preferred algorithm among the WWW-Authenticate headers.
func (p *softphone) pick(res *sip.Response) string {
	for _, h := range res.GetHeaders("WWW-Authenticate") {
		if strings.Contains(strings.ToLower(h.Value()), "algorithm="+strings.ToLower(p.alg)) {
			return h.Value()
		}
	}
	return ""
}

// register performs REGISTER with digest. Returns the final status code.
func (p *softphone) register(expires int) int {
	p.t.Helper()
	aor := sip.Uri{Scheme: "sip", User: p.ext, Host: domain}
	auth := ""
	for attempt := 0; attempt < 3; attempt++ {
		req := sip.NewRequest(sip.REGISTER, sip.Uri{Scheme: "sip", Host: domain})
		from := &sip.FromHeader{Address: aor, Params: sip.NewParams()}
		from.Params.Add("tag", sip.GenerateTagN(12))
		req.AppendHeader(from)
		req.AppendHeader(&sip.ToHeader{Address: aor, Params: sip.NewParams()})
		req.AppendHeader(&sip.ContactHeader{Address: sip.Uri{Scheme: "sip", User: p.ext, Host: "127.0.0.1", Port: p.port}})
		req.AppendHeader(sip.NewHeader("Expires", fmt.Sprint(expires)))
		if auth != "" {
			req.AppendHeader(sip.NewHeader("Authorization", auth))
		}
		req.SetDestination(sipHost)
		if p.tlsMode {
			req.SetTransport("TLS")
			req.SetDestination(strings.Split(sipHost, ":")[0] + ":5061")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		tx, err := p.client.TransactionRequest(ctx, req)
		if err != nil {
			cancel()
			p.t.Fatalf("register: %v", err)
		}
		var res *sip.Response
		select {
		case res = <-tx.Responses():
		case <-ctx.Done():
			cancel()
			p.t.Fatal("register: timeout")
		}
		cancel()
		if res.StatusCode == 401 {
			ch := p.pick(res)
			if ch == "" {
				p.t.Fatalf("no %s challenge offered", p.alg)
			}
			p.lastAuth = ch
			auth = p.authHeader(ch, "REGISTER", "sip:"+domain)
			continue
		}
		return int(res.StatusCode)
	}
	return 401
}

// invite places a call. Returns the dialog (nil if the call was refused) and the final status.
func (p *softphone) invite(toExt string) (*sipgo.DialogClientSession, *sdpmini.Audio, int) {
	p.t.Helper()
	uri := sip.Uri{Scheme: "sip", User: toExt, Host: domain}
	recipient := sip.Uri{Scheme: "sip", User: toExt, Host: strings.Split(sipHost, ":")[0], Port: 5060}
	_ = uri
	rtpPort := p.rtp.LocalAddr().(*net.UDPAddr).Port
	sdp := sdpmini.Build("127.0.0.1", rtpPort, time.Now().UnixNano()/1e6)
	if p.srtp {
		p.myKey = make([]byte, sdpmini.SRTPKeyLen)
		_, _ = cryptorand.Read(p.myKey)
		sdp = sdpmini.BuildSecure("127.0.0.1", rtpPort, time.Now().UnixNano()/1e6, p.myKey)
	}
	from := &sip.FromHeader{Address: sip.Uri{Scheme: "sip", User: p.ext, Host: domain}, Params: sip.NewParams()}
	from.Params.Add("tag", sip.GenerateTagN(12))
	headers := []sip.Header{from, &sip.ToHeader{Address: uri, Params: sip.NewParams()}, sip.NewHeader("Content-Type", "application/sdp")}
	if p.lastAuth != "" { // reuse the registration nonce (stateless on the server)
		headers = append(headers, sip.NewHeader("Authorization", p.authHeader(p.lastAuth, "INVITE", "sip:"+toExt+"@"+domain)))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	p.t.Cleanup(cancel)
	dlg, err := p.dialogC.Invite(ctx, recipient, sdp, headers...)
	if err != nil {
		p.t.Fatalf("invite: %v", err)
	}
	if err := dlg.WaitAnswer(ctx, sipgo.AnswerOptions{}); err != nil {
		code := 0
		var rerr *sipgo.ErrDialogResponse
		if asResp(err, &rerr) {
			code = int(rerr.Res.StatusCode)
		}
		_ = dlg.Close()
		return nil, nil, code
	}
	answer, err := sdpmini.Parse(dlg.InviteResponse.Body())
	if err != nil {
		p.t.Fatalf("answer SDP: %v", err)
	}
	if p.srtp {
		if !answer.Secure || answer.Key == nil {
			p.t.Fatalf("the server answered a SRTP offer without SRTP")
		}
		p.sendCtx, p.recvCtx = p.ctx(p.myKey), p.ctx(answer.Key)
	}
	if err := dlg.Ack(ctx); err != nil {
		p.t.Fatalf("ack: %v", err)
	}
	return dlg, &answer, 200
}

// ---- RTP helpers (the phone sends first so the media server can latch onto its real address) ----

func (p *softphone) sendRTP(to string, marker byte, stop <-chan struct{}) {
	addr, _ := net.ResolveUDPAddr("udp4", to)
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
				b, _ := (&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: seq, Timestamp: uint32(seq) * 160, SSRC: 4242}, Payload: bytes.Repeat([]byte{marker}, 160)}).Marshal()
				if p.sendCtx != nil {
					enc, err := p.sendCtx.EncryptRTP(nil, b, nil)
					if err != nil {
						continue
					}
					b = enc
				}
				_, _ = p.rtp.WriteToUDP(b, addr)
			}
		}
	}()
}

func (p *softphone) expectRTP(marker byte) {
	p.t.Helper()
	buf := make([]byte, 1600)
	deadline := time.Now().Add(10 * time.Second)
	for {
		_ = p.rtp.SetReadDeadline(deadline)
		n, _, err := p.rtp.ReadFromUDP(buf)
		if err != nil {
			p.t.Fatalf("phone %s never received audio marker %#x", p.ext, marker)
		}
		raw := buf[:n]
		if p.recvCtx != nil {
			if bytes.Contains(raw, bytes.Repeat([]byte{marker}, 20)) {
				p.t.Fatalf("audio is visible in clear on the wire although SRTP is on")
			}
			plain, err := p.recvCtx.DecryptRTP(nil, raw, nil)
			if err != nil {
				continue
			}
			raw = plain
		}
		var pkt rtp.Packet
		if pkt.Unmarshal(raw) == nil && len(pkt.Payload) > 0 && pkt.Payload[0] == marker {
			if pkt.PayloadType != 0 {
				p.t.Fatalf("phone received payload type %d, want 0", pkt.PayloadType)
			}
			return
		}
	}
}

func (p *softphone) ctx(key []byte) *srtp.Context {
	c, err := srtp.CreateContext(key[:16], key[16:], srtp.ProtectionProfileAes128CmHmacSha1_80)
	if err != nil {
		p.t.Fatal(err)
	}
	return c
}

// answerFor builds the phone's answer to the server's offer: SRTP when both sides support it.
func (p *softphone) answerFor(offer sdpmini.Audio) []byte {
	port := p.rtp.LocalAddr().(*net.UDPAddr).Port
	id := time.Now().UnixNano() / 1e6
	if p.srtp && offer.Secure && offer.Key != nil {
		p.myKey = make([]byte, sdpmini.SRTPKeyLen)
		_, _ = cryptorand.Read(p.myKey)
		p.sendCtx, p.recvCtx = p.ctx(p.myKey), p.ctx(offer.Key)
		return sdpmini.BuildSecure("127.0.0.1", port, id, p.myKey)
	}
	p.sendCtx, p.recvCtx = nil, nil
	return sdpmini.Build("127.0.0.1", port, id)
}

func asResp(err error, target **sipgo.ErrDialogResponse) bool { return errors.As(err, target) }
