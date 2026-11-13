//go:build e2e || load

// Load test: LOAD_USERS employees log in and stay connected, exchange chat messages, and LOAD_CALLS simultaneous
// PC-to-PC calls carry audio for LOAD_SECONDS. Prepare the users with scripts/load-users.sh and run it with
// scripts/load-test.sh (it also samples container CPU and memory). Thresholds at the bottom decide pass or fail.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}

type stats []time.Duration

func (s stats) pct(p float64) time.Duration {
	if len(s) == 0 {
		return 0
	}
	c := append(stats(nil), s...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	return c[int(float64(len(c)-1)*p)]
}

func (s stats) String() string {
	return fmt.Sprintf("n=%d p50=%v p95=%v max=%v", len(s), s.pct(.5).Round(time.Millisecond), s.pct(.95).Round(time.Millisecond), s.pct(1).Round(time.Millisecond))
}

type loadUser struct {
	name  string
	id    int64
	token string
	conn  *websocket.Conn
}

// result of an API call without a *testing.T (goroutine friendly).
func apiCall(token, method, path string, body any) (int, map[string]any, error) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
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
		return 0, nil, err
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out, nil
}

func parallel(n, workers int, f func(i int)) {
	var wg sync.WaitGroup
	idx := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				f(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		idx <- i
	}
	close(idx)
	wg.Wait()
}

func TestLoad(t *testing.T) {
	if os.Getenv("LOAD_USERS") == "" {
		t.Skip("load test: run scripts/load-test.sh")
	}
	nUsers, nCalls, seconds := envInt("LOAD_USERS", 500), envInt("LOAD_CALLS", 50), envInt("LOAD_SECONDS", 20)
	if nCalls*2 > nUsers {
		t.Fatalf("%d calls need %d users", nCalls, nCalls*2)
	}
	users := make([]*loadUser, nUsers)
	httpc.Timeout = 30 * time.Second

	// ---- 1. everybody logs in (clock-in) ----
	var loginLat stats
	var mu sync.Mutex
	var loginFail int32
	parallel(nUsers, 40, func(i int) {
		name := fmt.Sprintf("load%04d", i+1)
		t0 := time.Now()
		code, out, err := apiCall("", "POST", "/api/auth/login", map[string]string{"username": name, "password": "Demo-Passw0rd!"})
		if err != nil || code != 200 {
			atomic.AddInt32(&loginFail, 1)
			t.Logf("login %s failed: code=%d err=%v body=%v", name, code, err, out)
			return
		}
		u := &loadUser{name: name, id: int64(out["user"].(map[string]any)["id"].(float64)), token: out["access_token"].(string)}
		users[i] = u
		mu.Lock()
		loginLat = append(loginLat, time.Since(t0))
		mu.Unlock()
		_, _, _ = apiCall(u.token, "PUT", "/api/presence", map[string]string{"status": "on"})
	})
	t.Logf("LOGIN   %d/%d ok, failures=%d, latency %v", len(loginLat), nUsers, loginFail, loginLat)

	// ---- 2. everybody keeps a WebSocket open ----
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type arrival struct {
		msgID int64
		at    time.Time
	}
	arrivals := make(chan arrival, nUsers*4)
	var wsOK int32
	parallel(nUsers, 60, func(i int) {
		u := users[i]
		if u == nil {
			return
		}
		conn, _, err := websocket.Dial(ctx, strings.Replace(base, "https", "wss", 1)+"/api/ws", &websocket.DialOptions{HTTPClient: httpc})
		if err != nil {
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","token":"`+u.token+`"}`))
		ready := make(chan struct{})
		go func() {
			once := false
			for {
				_, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var ev struct {
					Type string `json:"type"`
					Data struct {
						ID       int64 `json:"id"`
						SenderID int64 `json:"sender_id"`
					} `json:"data"`
				}
				if json.Unmarshal(data, &ev) != nil {
					continue
				}
				if ev.Type == "ready" && !once {
					once = true
					close(ready)
				}
				if ev.Type == "message" && ev.Data.SenderID != u.id {
					arrivals <- arrival{ev.Data.ID, time.Now()}
				}
			}
		}()
		select {
		case <-ready:
			u.conn = conn
			atomic.AddInt32(&wsOK, 1)
		case <-time.After(15 * time.Second):
		}
	})
	t.Logf("SOCKETS %d/%d connected", wsOK, nUsers)

	// ---- 3. chat: everybody writes to the next employee ----
	sentAt := sync.Map{}
	var sendFail int32
	var chatSent int32
	parallel(nUsers, 40, func(i int) {
		a, b := users[i], users[(i+1)%nUsers]
		if a == nil || b == nil {
			return
		}
		code, conv, err := apiCall(a.token, "POST", "/api/conversations", map[string]any{"member_ids": []int64{b.id}})
		if err != nil || code != 200 {
			atomic.AddInt32(&sendFail, 1)
			return
		}
		t0 := time.Now()
		code, msg, err := apiCall(a.token, "POST", fmt.Sprintf("/api/conversations/%d/messages", int64(conv["id"].(float64))), map[string]string{"body": "charge " + a.name})
		if err != nil || code != 200 {
			atomic.AddInt32(&sendFail, 1)
			return
		}
		sentAt.Store(int64(msg["id"].(float64)), t0)
		atomic.AddInt32(&chatSent, 1)
	})
	var chatLat stats
	deadline := time.After(10 * time.Second)
	for received := int32(0); received < chatSent; {
		select {
		case a := <-arrivals:
			if t0, ok := sentAt.Load(a.msgID); ok {
				chatLat = append(chatLat, a.at.Sub(t0.(time.Time)))
				received++
			}
		case <-deadline:
			received = chatSent
		}
	}
	t.Logf("CHAT    sent=%d delivered=%d lost=%d send_failures=%d latency %v", chatSent, len(chatLat), int(chatSent)-len(chatLat), sendFail, chatLat)

	// ---- 4. simultaneous calls with audio ----
	type callResult struct {
		setup     time.Duration
		ok        bool
		got, want int64
	}
	results := make([]callResult, nCalls)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for c := 0; c < nCalls; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			caller, callee := users[c], users[nUsers/2+c]
			if caller == nil || callee == nil {
				return
			}
			<-start
			t0 := time.Now()
			code, out, err := apiCall(caller.token, "POST", "/api/calls", map[string]any{"to_user_id": callee.id})
			if err != nil || code != 200 || out["state"] != "ringing" {
				return
			}
			id := fmt.Sprint(out["id"])
			if code, _, err := apiCall(callee.token, "POST", "/api/calls/"+id+"/answer", nil); err != nil || code != 200 {
				return
			}
			var wgl sync.WaitGroup
			var recv int64
			connected := make(chan struct{}, 2)
			stop := make(chan struct{})
			for _, p := range []*loadUser{caller, callee} {
				wgl.Add(1)
				go func(p *loadUser) {
					defer wgl.Done()
					pc, err := loadLeg(p.token, id, &recv, connected, stop)
					if err != nil {
						return
					}
					defer pc.Close()
					<-stop
				}(p)
			}
			for i := 0; i < 2; i++ {
				select {
				case <-connected:
				case <-time.After(20 * time.Second):
					close(stop)
					wgl.Wait()
					return
				}
			}
			results[c].setup = time.Since(t0)
			time.Sleep(time.Duration(seconds) * time.Second)
			close(stop)
			wgl.Wait()
			_, _, _ = apiCall(caller.token, "POST", "/api/calls/"+id+"/hangup", nil)
			results[c].ok = true
			results[c].got = atomic.LoadInt64(&recv)
			results[c].want = int64(2 * seconds * 50) // two legs, 50 packets per second
		}(c)
	}
	close(start)
	wg.Wait()

	var setup stats
	established := 0
	var lossMax, lossSum float64
	for _, r := range results {
		if !r.ok {
			continue
		}
		established++
		setup = append(setup, r.setup)
		loss := 100 * float64(r.want-r.got) / float64(r.want)
		if loss < 0 {
			loss = 0
		}
		lossSum += loss
		if loss > lossMax {
			lossMax = loss
		}
	}
	lossAvg := 0.0
	if established > 0 {
		lossAvg = lossSum / float64(established)
	}
	t.Logf("CALLS   established=%d/%d for %ds, setup %v, RTP loss avg=%.2f%% max=%.2f%%", established, nCalls, seconds, setup, lossAvg, lossMax)

	// ---- thresholds ----
	if int(loginFail) > nUsers/100 {
		t.Errorf("more than 1%% of logins failed (%d)", loginFail)
	}
	if int(wsOK) < nUsers-nUsers/100 {
		t.Errorf("only %d/%d sockets connected", wsOK, nUsers)
	}
	if lost := int(chatSent) - len(chatLat); lost > 0 || chatLat.pct(.95) > time.Second {
		t.Errorf("chat: %d lost, p95=%v (limit 1s)", lost, chatLat.pct(.95))
	}
	if established < nCalls || setup.pct(.95) > 5*time.Second {
		t.Errorf("calls: %d/%d established, setup p95=%v (limit 5s)", established, nCalls, setup.pct(.95))
	}
	if lossAvg > 5 {
		t.Errorf("average RTP loss %.2f%% (limit 5%%)", lossAvg)
	}
}

// loadLeg joins the media service for a call, sends Opus-sized RTP at 50 packets per second and counts what it receives.
func loadLeg(token, callID string, recv *int64, connected chan<- struct{}, stop <-chan struct{}) (*webrtc.PeerConnection, error) {
	code, tok, err := apiCall(token, "GET", "/api/calls/"+callID+"/token", nil)
	if err != nil || code != 200 {
		return nil, fmt.Errorf("token")
	}
	me := &webrtc.MediaEngine{}
	opus := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}
	_ = me.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: opus, PayloadType: 111}, webrtc.RTPCodecTypeAudio)
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(me)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	out, _ := webrtc.NewTrackLocalStaticRTP(opus, "audio", "load")
	_, _ = pc.AddTrack(out)
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			if _, _, err := tr.ReadRTP(); err != nil {
				return
			}
			atomic.AddInt64(recv, 1)
		}
	})
	var once sync.Once
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			once.Do(func() {
				connected <- struct{}{}
				go func() {
					tick := time.NewTicker(20 * time.Millisecond)
					defer tick.Stop()
					seq := uint16(0)
					for {
						select {
						case <-stop:
							return
						case <-tick.C:
							seq++
							_ = out.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: seq, Timestamp: uint32(seq) * 960}, Payload: make([]byte, 60)})
						}
					}
				}()
			})
		}
	})
	offer, _ := pc.CreateOffer(nil)
	gathered := webrtc.GatheringCompletePromise(pc)
	_ = pc.SetLocalDescription(offer)
	<-gathered
	b, _ := json.Marshal(map[string]string{"sdp": pc.LocalDescription().SDP})
	req, _ := http.NewRequest("POST", base+"/media/calls/"+callID+"/offer", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+fmt.Sprint(tok["token"]))
	res, err := httpc.Do(req)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	defer res.Body.Close()
	var ans struct{ SDP string }
	_ = json.NewDecoder(res.Body).Decode(&ans)
	if res.StatusCode != 200 {
		_ = pc.Close()
		return nil, fmt.Errorf("offer %d", res.StatusCode)
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans.SDP}); err != nil {
		_ = pc.Close()
		return nil, err
	}
	return pc, nil
}
