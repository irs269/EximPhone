package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/irs269/eximphone/services/pkg/crypto"
	"github.com/irs269/eximphone/services/pkg/sdpmini"

	"github.com/minio/minio-go/v7"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

// Compliance recording. Each direction of the call is written to its own file as it arrives (G.711 as
// WAV, Opus as Ogg) so nothing is decoded or transcoded; when the call ends the files are sealed with
// AES-256-GCM, uploaded to object storage and registered with the api. Plain audio only ever exists in a
// temporary file of the media container while the call is running.

const maxRecordSeconds = 4 * 3600 // safety cap per call

type recordingConfig struct {
	CallerUserID int64 `json:"caller_user_id"`
	CalleeUserID int64 `json:"callee_user_id"`
}

// recorderEnv groups what finalisation needs.
type recorderEnv struct {
	box    *crypto.Box
	s3     *minio.Client
	bucket string
	apiURL string
	token  string
	tmpDir string
	log    interface {
		Warn(string, ...any)
		Info(string, ...any)
	}
	httpc http.Client
}

type trackWriter struct {
	user   int64
	codec  string // pcmu | pcma | opus
	ext    string
	file   *os.File
	ogg    *oggwriter.OggWriter
	first  uint32
	last   uint32
	next   uint32 // expected next RTP timestamp (G.711 gap filling)
	bytes  int64
	seen   bool
	closed bool
}

type recorder struct {
	callID string
	cfg    recordingConfig
	env    *recorderEnv

	mu     sync.Mutex
	tracks map[int64]*trackWriter
	start  time.Time
	done   bool
}

func newRecorder(callID string, cfg recordingConfig, env *recorderEnv) *recorder {
	return &recorder{callID: callID, cfg: cfg, env: env, tracks: map[int64]*trackWriter{}, start: time.Now()}
}

// write records one RTP packet received from the given user.
func (r *recorder) write(user int64, pkt *rtp.Packet) {
	if pkt.PayloadType == sdpmini.PTDTMF || len(pkt.Payload) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done || time.Since(r.start) > maxRecordSeconds*time.Second {
		return
	}
	t := r.tracks[user]
	if t == nil {
		var err error
		if t, err = r.open(user, pkt); err != nil {
			r.env.log.Warn("recording disabled for a track", "call", r.callID, "err", err.Error())
			t = &trackWriter{user: user, closed: true}
			r.tracks[user] = t
			return
		}
		r.tracks[user] = t
	}
	if t.closed {
		return
	}
	if t.ogg != nil {
		_ = t.ogg.WriteRTP(pkt)
		t.last = pkt.Timestamp
		return
	}
	// G.711: fill timestamp gaps (packet loss, silence suppression) with silence so both tracks stay on the same clock.
	if gap := int64(int32(pkt.Timestamp - t.next)); t.seen && gap > 0 && gap < 8000*30 {
		silence := byte(0xFF) // mu-law silence
		if t.codec == "pcma" {
			silence = 0xD5
		}
		_, _ = t.file.Write(bytes.Repeat([]byte{silence}, int(gap)))
		t.bytes += gap
	}
	if !t.seen {
		t.first, t.seen = pkt.Timestamp, true
	}
	n, _ := t.file.Write(pkt.Payload)
	t.bytes += int64(n)
	t.next = pkt.Timestamp + uint32(len(pkt.Payload))
	t.last = pkt.Timestamp
}

func (r *recorder) open(user int64, pkt *rtp.Packet) (*trackWriter, error) {
	f, err := os.CreateTemp(r.env.tmpDir, "rec-*")
	if err != nil {
		return nil, err
	}
	t := &trackWriter{user: user, file: f}
	switch pkt.PayloadType {
	case sdpmini.PTPCMU:
		t.codec, t.ext = "pcmu", "wav"
	case sdpmini.PTPCMA:
		t.codec, t.ext = "pcma", "wav"
	default:
		t.codec, t.ext = "opus", "ogg"
	}
	if t.codec == "opus" {
		if t.ogg, err = oggwriter.NewWith(f, 48000, 2); err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return nil, err
		}
		t.first = pkt.Timestamp
		t.seen = true
	} else if _, err := f.Write(make([]byte, wavHeaderSize)); err != nil { // header is written at the end
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, err
	}
	return t, nil
}

const wavHeaderSize = 58 // RIFF header + fmt (18 bytes) + fact + data chunk header

// wavHeader describes G.711 (format 7 = mu-law, 6 = A-law), 8 kHz mono.
func wavHeader(codec string, dataLen int64) []byte {
	format := uint16(7)
	if codec == "pcma" {
		format = 6
	}
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(wavHeaderSize - 8 + dataLen))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	w(uint32(18))
	w(format)
	w(uint16(1))    // channels
	w(uint32(8000)) // sample rate
	w(uint32(8000)) // byte rate
	w(uint16(1))    // block align
	w(uint16(8))    // bits per sample
	w(uint16(0))    // extra format bytes
	b.WriteString("fact")
	w(uint32(4))
	w(uint32(dataLen))
	b.WriteString("data")
	w(uint32(dataLen))
	return b.Bytes()
}

type uploaded struct {
	Track    string `json:"track"`
	Codec    string `json:"codec"`
	S3Key    string `json:"s3_key"`
	Bytes    int64  `json:"bytes"`
	Duration int    `json:"duration_sec"`
}

// finish closes the files, seals and uploads them, then tells the api. Safe to call once; later calls do nothing.
func (r *recorder) finish(ctx context.Context) {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return
	}
	r.done = true
	tracks := r.tracks
	r.mu.Unlock()

	var out []uploaded
	for user, t := range tracks {
		if t.closed || t.file == nil {
			continue
		}
		name := t.file.Name()
		var dur int
		if t.ogg != nil {
			_ = t.ogg.Close()
			dur = int(t.last-t.first) / 48000
		} else {
			_, _ = t.file.WriteAt(wavHeader(t.codec, t.bytes), 0)
			dur = int(t.bytes / 8000)
		}
		_ = t.file.Close()
		plain, err := os.ReadFile(name)
		_ = os.Remove(name)
		if err != nil || dur < 1 {
			continue // nothing worth keeping (call never carried audio)
		}
		track := "callee"
		if user == r.cfg.CallerUserID {
			track = "caller"
		}
		key := fmt.Sprintf("%s/%s.%s.enc", r.callID, track, t.ext)
		sealed, err := r.env.box.Seal(plain, []byte(key)) // the key is bound as additional data: objects cannot be swapped
		if err != nil {
			r.env.log.Warn("recording seal failed", "call", r.callID, "err", err.Error())
			continue
		}
		if _, err := r.env.s3.PutObject(ctx, r.env.bucket, key, bytes.NewReader(sealed), int64(len(sealed)), minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
			r.env.log.Warn("recording upload failed", "call", r.callID, "err", err.Error())
			continue
		}
		out = append(out, uploaded{Track: track, Codec: t.codec, S3Key: key, Bytes: int64(len(plain)), Duration: dur})
	}
	if len(out) == 0 {
		return
	}
	body, _ := json.Marshal(map[string]any{"call_id": r.callID, "tracks": out})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.env.apiURL+"/internal/recordings", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", r.env.token)
	res, err := r.env.httpc.Do(req)
	if err != nil {
		r.env.log.Warn("recording registration failed", "call", r.callID, "err", err.Error())
		return
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	r.env.log.Info("call recorded", "call", r.callID, "tracks", len(out))
}
