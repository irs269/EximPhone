// Package sdpmini reads and writes the small subset of SDP (RFC 4566) needed to bridge
// G.711 audio between SIP phones and the media service.
package sdpmini

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	PTPCMU = 0   // G.711 mu-law
	PTPCMA = 8   // G.711 A-law
	PTDTMF = 101 // telephone-event (RFC 4733)
)

// Audio describes the audio line of a remote SDP.
type Audio struct {
	Addr    string // connection address (c=)
	Port    int
	Codecs  []int
	DTMFPT  int // payload type of telephone-event, 0 if not offered
	Offered map[int]bool
	Secure  bool   // RTP/SAVP: the phone wants SRTP
	Key     []byte // SDES master key + salt (30 bytes, AES_CM_128_HMAC_SHA1_80) of the sender, nil if absent
}

// SRTPKeyLen is the SDES key material length for AES_CM_128_HMAC_SHA1_80: 16 byte key + 14 byte salt.
const SRTPKeyLen = 30

func (a Audio) Has(pt int) bool { return a.Offered[pt] }

// Parse extracts the first audio media line. Hold (a=sendonly/0.0.0.0) is not interpreted.
func Parse(sdp []byte) (Audio, error) {
	a := Audio{Offered: map[int]bool{}}
	var sessionAddr string
	inAudio := false
	for _, line := range strings.FieldsFunc(string(sdp), func(r rune) bool { return r == '\n' || r == '\r' }) {
		if len(line) < 2 || line[1] != '=' {
			continue
		}
		val := line[2:]
		switch line[0] {
		case 'c':
			f := strings.Fields(val) // IN IP4 1.2.3.4
			if len(f) >= 3 {
				if inAudio {
					a.Addr = f[2]
				} else {
					sessionAddr = f[2]
				}
			}
		case 'm':
			if inAudio { // only the first audio section
				continue
			}
			f := strings.Fields(val) // audio 4000 RTP/AVP 0 8 101
			if len(f) < 4 || f[0] != "audio" {
				continue
			}
			port, err := strconv.Atoi(f[1])
			if err != nil {
				return a, fmt.Errorf("bad port")
			}
			a.Port, inAudio = port, true
			a.Secure = strings.Contains(f[2], "SAVP")
			for _, p := range f[3:] {
				if pt, err := strconv.Atoi(p); err == nil {
					a.Codecs = append(a.Codecs, pt)
					a.Offered[pt] = true
				}
			}
		case 'a':
			if inAudio && strings.HasPrefix(val, "crypto:") && a.Key == nil {
				// a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:<base64 key||salt>[|lifetime|mki]
				f := strings.Fields(val[7:])
				if len(f) >= 3 && f[1] == "AES_CM_128_HMAC_SHA1_80" && strings.HasPrefix(f[2], "inline:") {
					b64, _, _ := strings.Cut(f[2][7:], "|")
					if raw, err := base64.StdEncoding.DecodeString(b64); err == nil && len(raw) == SRTPKeyLen {
						a.Key = raw
					}
				}
			}
			if inAudio && strings.HasPrefix(val, "rtpmap:") && strings.Contains(val, "telephone-event") {
				if pt, err := strconv.Atoi(strings.Fields(val[7:])[0]); err == nil {
					a.DTMFPT = pt
				}
			}
		}
	}
	if !inAudio {
		return a, fmt.Errorf("no audio media line")
	}
	if a.Addr == "" {
		a.Addr = sessionAddr
	}
	if a.Addr == "" {
		return a, fmt.Errorf("no connection address")
	}
	return a, nil
}

// Build returns an SDP offering/answering PCMU plus telephone-event at ip:port.
func Build(ip string, port int, sessionID int64) []byte {
	return []byte(strings.Join([]string{
		"v=0",
		fmt.Sprintf("o=eximphone %d %d IN IP4 %s", sessionID, sessionID, ip),
		"s=EximPhone",
		"c=IN IP4 " + ip,
		"t=0 0",
		fmt.Sprintf("m=audio %d RTP/AVP %d %d", port, PTPCMU, PTDTMF),
		"a=rtpmap:0 PCMU/8000",
		"a=rtpmap:101 telephone-event/8000",
		"a=fmtp:101 0-16",
		"a=ptime:20",
		"a=sendrecv",
		"",
	}, "\r\n"))
}

// BuildSecure is Build for SRTP (SDES, RFC 4568): RTP/SAVP with the sender's own key material.
func BuildSecure(ip string, port int, sessionID int64, key []byte) []byte {
	plain := string(Build(ip, port, sessionID))
	plain = strings.Replace(plain, "RTP/AVP", "RTP/SAVP", 1)
	crypto := "a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:" + base64.StdEncoding.EncodeToString(key) + "\r\n"
	return []byte(strings.Replace(plain, "a=ptime:20\r\n", crypto+"a=ptime:20\r\n", 1))
}
