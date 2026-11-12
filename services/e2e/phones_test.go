//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/irs269/eximphone/services/pkg/sdpmini"

	"github.com/emiago/sipgo"
)

func asResp(err error, target **sipgo.ErrDialogResponse) bool { return errors.As(err, target) }

type account struct{ id, user, pass string }

func createDevice(t *testing.T, admin string, userID int64, typ, vendor, mac string) account {
	t.Helper()
	body := map[string]any{"user_id": userID, "type": typ, "vendor": vendor}
	if mac != "" {
		body["mac"] = mac
	}
	code, out := call(t, admin, "POST", "/api/admin/devices", body)
	if code != 200 {
		t.Fatalf("create device: %d %v", code, out)
	}
	return account{id: fmt.Sprint(out["id"]), user: fmt.Sprint(out["sip_username"]), pass: fmt.Sprint(out["sip_password"])}
}

func userID(t *testing.T, token, username string) (int64, string) {
	t.Helper()
	_, out := call(t, token, "GET", "/api/users", nil)
	for _, u := range out["users"].([]any) {
		m := u.(map[string]any)
		if m["username"] == username {
			return int64(m["id"].(float64)), fmt.Sprint(m["extension"])
		}
	}
	t.Fatalf("user %s not found", username)
	return 0, ""
}

func hangupAll(t *testing.T, tokens ...string) {
	for _, tok := range tokens {
		_, out := call(t, tok, "GET", "/api/calls", nil)
		if list, ok := out["calls"].([]any); ok {
			for _, c := range list {
				m := c.(map[string]any)
				if m["state"] != "ended" {
					call(t, tok, "POST", "/api/calls/"+fmt.Sprint(m["id"])+"/hangup", nil)
				}
			}
		}
	}
}

func waitInvite(t *testing.T, p *softphone) *incoming {
	t.Helper()
	select {
	case in := <-p.invites:
		return in
	case <-time.After(8 * time.Second):
		t.Fatalf("phone %s never received an INVITE", p.ext)
		return nil
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

func TestPhones(t *testing.T) {
	admin := login(t, "asaid") // it_admin (bootstrapped by INITIAL_IT_ADMINS)
	omar := login(t, "oabdallah")
	mariam := login(t, "mjuma")
	idrissa := login(t, "imoussa")

	// Start from a clean slate: disable devices left by previous runs.
	_, list := call(t, admin.token, "GET", "/api/admin/devices", nil)
	if devs, ok := list["devices"].([]any); ok {
		for _, d := range devs {
			m := d.(map[string]any)
			if m["active"] == true {
				call(t, admin.token, "DELETE", "/api/admin/devices/"+fmt.Sprint(int(m["id"].(float64))), nil)
			}
		}
	}
	hangupAll(t, omar.token, mariam.token, idrissa.token)

	devM := createDevice(t, admin.token, mariam.id, "softphone", "generic", "")
	devI := createDevice(t, admin.token, idrissa.id, "softphone", "generic", "")
	phoneM := newSoftphone(t, mariam.ext, devM.user, devM.pass, "SHA-256", 5090)
	phoneI := newSoftphone(t, idrissa.ext, devI.user, devI.pass, "MD5", 5091)

	t.Run("digest registration (SHA-256 and MD5)", func(t *testing.T) {
		if code := phoneM.register(300); code != 200 {
			t.Fatalf("SHA-256 register: %d", code)
		}
		if code := phoneI.register(300); code != 200 {
			t.Fatalf("MD5 register: %d", code)
		}
		bad := newSoftphone(t, mariam.ext, devM.user, "wrong-password", "SHA-256", 5092)
		if code := bad.register(300); code != 403 {
			t.Fatalf("wrong password must be refused with 403, got %d", code)
		}
		// an account cannot act as another extension
		thief := newSoftphone(t, idrissa.ext, devM.user, devM.pass, "SHA-256", 5093)
		if code := thief.register(300); code != 403 {
			t.Fatalf("account used for another extension: %d, want 403", code)
		}
	})

	t.Run("desk phone calls a PC", func(t *testing.T) {
		hangupAll(t, omar.token, mariam.token, idrissa.token)
		type result struct {
			dlg *sipgo.DialogClientSession
			ans string
			rtp string
			err int
		}
		ch := make(chan result, 1)
		go func() {
			dlg, ans, code := phoneM.invite(omar.ext)
			if ans == nil {
				ch <- result{err: code}
				return
			}
			ch <- result{dlg: dlg, rtp: fmt.Sprintf("%s:%d", ans.Addr, ans.Port)}
		}()
		inc := omar.wait("call_incoming", func(d map[string]any) bool { return int64(d["from"].(map[string]any)["id"].(float64)) == mariam.id })
		id := fmt.Sprint(inc["call_id"])
		if code, _ := call(t, omar.token, "POST", "/api/calls/"+id+"/answer", nil); code != 200 {
			t.Fatalf("answer: %d", code)
		}
		r := <-ch
		if r.dlg == nil {
			t.Fatalf("phone call failed with status %d", r.err)
		}
		leg := omar.joinMedia(id)
		stop := make(chan struct{})
		defer close(stop)
		phoneM.sendRTP(r.rtp, 0xA1, stop)
		leg.stream(0xB1, stop)
		leg.expect(t, 0xA1)
		phoneM.expectRTP(0xB1)
		t.Log("audio flows both ways between the desk phone and the PC (G.711 bridge)")

		call(t, omar.token, "POST", "/api/calls/"+id+"/hangup", nil)
		eventually(t, "phone receives BYE after the PC hangs up", func() bool { _, b := phoneM.counts(); return b >= 1 })
	})

	t.Run("PC calls a desk phone (forking: phone answers, PC stops ringing)", func(t *testing.T) {
		hangupAll(t, omar.token, mariam.token, idrissa.token)
		code, out := call(t, omar.token, "POST", "/api/calls", map[string]any{"to_user_id": mariam.id})
		if code != 200 || out["state"] != "ringing" {
			t.Fatalf("call: %d %v", code, out)
		}
		id := fmt.Sprint(out["id"])
		in := waitInvite(t, phoneM)                                                             // the desk phone rings
		mariam.wait("call_incoming", func(d map[string]any) bool { return d["call_id"] == id }) // ...and so does her PC
		sdp := sdpAnswer(phoneM)
		in.answer <- sdp
		mariam.wait("call_answered", func(d map[string]any) bool { return d["call_id"] == id && d["via"] == "sip" })
		omar.wait("call_answered", func(d map[string]any) bool { return d["call_id"] == id })
		leg := omar.joinMedia(id)
		stop := make(chan struct{})
		defer close(stop)
		phoneM.sendRTP(fmt.Sprintf("%s:%d", in.offer.Addr, in.offer.Port), 0xC3, stop)
		leg.stream(0xD4, stop)
		leg.expect(t, 0xC3)
		phoneM.expectRTP(0xD4)
		t.Log("PC to desk phone: audio flows both ways")

		call(t, omar.token, "POST", "/api/calls/"+id+"/hangup", nil)
		<-in.done
	})

	t.Run("PC answers first: the desk phone gets CANCEL", func(t *testing.T) {
		hangupAll(t, omar.token, mariam.token, idrissa.token)
		before, _ := phoneM.counts()
		_, out := call(t, omar.token, "POST", "/api/calls", map[string]any{"to_user_id": mariam.id})
		id := fmt.Sprint(out["id"])
		in := waitInvite(t, phoneM)
		mariam.wait("call_incoming", func(d map[string]any) bool { return d["call_id"] == id })
		if code, _ := call(t, mariam.token, "POST", "/api/calls/"+id+"/answer", nil); code != 200 {
			t.Fatalf("answer: %d", code)
		}
		select {
		case <-in.done: // the ringing INVITE was cancelled by the server
			t.Log("the ringing INVITE on the desk phone was cancelled")
		case <-time.After(8 * time.Second):
			t.Fatal("INVITE on the desk phone was not cancelled after the PC answered")
		}
		_ = before
		call(t, omar.token, "POST", "/api/calls/"+id+"/hangup", nil)
	})

	t.Run("do not disturb, unknown extension, offline", func(t *testing.T) {
		call(t, mariam.token, "PUT", "/api/presence", map[string]string{"status": "dnd"})
		if _, _, code := phoneI.invite(mariam.ext); code != 486 {
			t.Fatalf("DND callee: %d, want 486 Busy Here", code)
		}
		call(t, mariam.token, "PUT", "/api/presence", map[string]string{"status": "on"})
		if _, _, code := phoneI.invite("9999"); code != 404 {
			t.Fatalf("unknown extension: %d, want 404", code)
		}
		_, offExt := userID(t, admin.token, "ysalim") // never logged in during this run
		call(t, admin.token, "PUT", "/api/presence", map[string]string{"status": "on"})
		if _, _, code := phoneI.invite(offExt); code != 480 {
			t.Fatalf("offline callee: %d, want 480 Temporarily Unavailable", code)
		}
	})

	t.Run("desk phone to desk phone", func(t *testing.T) {
		hangupAll(t, omar.token, mariam.token, idrissa.token)
		type result struct {
			dlg *sipgo.DialogClientSession
			rtp string
			err int
		}
		ch := make(chan result, 1)
		go func() {
			dlg, ans, code := phoneI.invite(mariam.ext)
			if ans == nil {
				ch <- result{err: code}
				return
			}
			ch <- result{dlg: dlg, rtp: fmt.Sprintf("%s:%d", ans.Addr, ans.Port)}
		}()
		in := waitInvite(t, phoneM)
		in.answer <- sdpAnswer(phoneM)
		r := <-ch
		if r.dlg == nil {
			t.Fatalf("call failed: %d", r.err)
		}
		stop := make(chan struct{})
		defer close(stop)
		phoneI.sendRTP(r.rtp, 0x71, stop)
		phoneM.sendRTP(fmt.Sprintf("%s:%d", in.offer.Addr, in.offer.Port), 0x82, stop)
		phoneM.expectRTP(0x71)
		phoneI.expectRTP(0x82)
		t.Log("audio flows both ways between two desk phones (anchored in media)")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.dlg.Bye(ctx)
		select {
		case <-in.done:
		case <-time.After(5 * time.Second):
			t.Fatal("callee did not get the BYE")
		}
	})

	t.Run("provisioning by MAC", func(t *testing.T) {
		mac := "00:15:65:AA:BB:CC"
		acc := createDevice(t, admin.token, idrissa.id, "desk_phone", "yealink", mac)
		res, err := httpc.Get(base + "/prov/001565aabbcc.cfg")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		body := string(b)
		for _, want := range []string{"account.1.user_name = " + idrissa.ext, "account.1.auth_name = " + acc.user, "account.1.password = " + acc.pass, "account.1.codec.pcmu.enable = 1"} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q in:\n%s", want, body)
			}
		}
		if res, _ := httpc.Get(base + "/prov/ffffffffffff.cfg"); res.StatusCode != http.StatusNotFound {
			t.Fatalf("unknown MAC: %d, want 404", res.StatusCode)
		}
		if res, _ := httpc.Get(base + "/api/internal/calls"); res.StatusCode != http.StatusNotFound {
			t.Fatalf("/internal must not be reachable through nginx: %d", res.StatusCode)
		}
		// a non-admin cannot create devices
		if code, _ := call(t, omar.token, "POST", "/api/admin/devices", map[string]any{"user_id": omar.id, "type": "softphone"}); code != 403 {
			t.Fatalf("non-admin device creation: %d, want 403", code)
		}
	})
}

// sdpAnswer is the desk phone's answer: its own RTP port, PCMU.
func sdpAnswer(p *softphone) []byte {
	return sdpmini.Build("127.0.0.1", p.rtp.LocalAddr().(*net.UDPAddr).Port, time.Now().UnixNano()/1e6)
}
