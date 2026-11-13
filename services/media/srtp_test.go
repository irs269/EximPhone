package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/irs269/eximphone/services/pkg/sdpmini"

	"github.com/pion/rtp"
	"github.com/pion/srtp/v3"
)

func mustCtx(t *testing.T, key []byte) *srtp.Context {
	t.Helper()
	c, err := srtp.CreateContext(key[:16], key[16:], srtp.ProtectionProfileAes128CmHmacSha1_80)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A phone using SRTP (SDES keys exchanged in SDP) talks to a browser: audio is encrypted on the phone side only,
// and packets that are not valid SRTP are dropped.
func TestSecurePhoneLegEncryptsAndRejectsPlainRTP(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	code, leg := internalPost(t, srv, "/legs", map[string]any{"call_id": "secure", "srtp": true})
	if code != 200 {
		t.Fatalf("leg: %d", code)
	}
	ourKey, err := base64.StdEncoding.DecodeString(leg["srtp_key"].(string))
	if err != nil || len(ourKey) != sdpmini.SRTPKeyLen {
		t.Fatalf("media must hand out 30 bytes of key material, got %v %v", len(ourKey), err)
	}
	legPort := int(leg["port"].(float64))

	phoneKey := make([]byte, sdpmini.SRTPKeyLen)
	_, _ = rand.Read(phoneKey)
	phone, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()

	// activation without the phone's key must be refused
	if code, _ := internalPost(t, srv, "/legs/"+leg["leg_id"].(string)+"/activate", map[string]string{"remote": phone.LocalAddr().String()}); code != http.StatusBadRequest {
		t.Fatalf("a secure leg activated without a key: %d", code)
	}
	if code, _ := internalPost(t, srv, "/legs/"+leg["leg_id"].(string)+"/activate",
		map[string]string{"remote": phone.LocalAddr().String(), "srtp_key": base64.StdEncoding.EncodeToString(phoneKey)}); code != http.StatusNoContent {
		t.Fatalf("activate: %d", code)
	}

	browser := dialWith(t, srv, "secure", 9, pcmuCap, 0)
	stop := make(chan struct{})
	defer close(stop)
	browser.stream(0xC9, stop)

	phoneEnc := mustCtx(t, phoneKey) // what the phone sends with
	phoneDec := mustCtx(t, ourKey)   // what the phone receives with
	target := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: legPort}

	// 1) plain RTP must be dropped
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
				p := &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: seq, Timestamp: uint32(seq) * 160, SSRC: 555}, Payload: bytes.Repeat([]byte{0xBA}, 160)}
				b, _ := p.Marshal()
				_, _ = phone.WriteToUDP(b, target) // unencrypted: an attacker or a misconfigured phone
			}
		}
	}()
	deadline := time.After(1500 * time.Millisecond)
loop:
	for {
		select {
		case p := <-browser.got:
			if len(p) > 0 && p[0] == 0xBA {
				t.Fatal("plain RTP was accepted on a secure leg")
			}
		case <-deadline:
			break loop
		}
	}

	// 2) properly protected SRTP reaches the browser
	go func() {
		seq := uint16(1000)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				seq++
				p := &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: seq, Timestamp: uint32(seq) * 160, SSRC: 777}, Payload: bytes.Repeat([]byte{0xD7}, 160)}
				b, _ := p.Marshal()
				enc, err := phoneEnc.EncryptRTP(nil, b, nil)
				if err == nil {
					_, _ = phone.WriteToUDP(enc, target)
				}
			}
		}
	}()
	expectMarker(t, browser, 0xD7)

	// 3) what the phone receives is SRTP: the marker is not visible on the wire, and it decrypts with our key
	buf := make([]byte, 1600)
	deadline2 := time.Now().Add(8 * time.Second)
	for {
		_ = phone.SetReadDeadline(deadline2)
		n, _, err := phone.ReadFromUDP(buf)
		if err != nil {
			t.Fatal("the phone never received the browser audio")
		}
		if bytes.Contains(buf[:n], bytes.Repeat([]byte{0xC9}, 20)) {
			t.Fatal("audio is visible in clear on the SRTP leg")
		}
		plain, err := phoneDec.DecryptRTP(nil, buf[:n], nil)
		if err != nil {
			continue
		}
		var p rtp.Packet
		if p.Unmarshal(plain) == nil && len(p.Payload) > 0 && p.Payload[0] == 0xC9 {
			break
		}
	}
}
