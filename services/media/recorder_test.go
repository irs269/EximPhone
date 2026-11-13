package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/irs269/eximphone/services/pkg/crypto"

	"github.com/pion/rtp"
)

func TestWavHeaderDescribesG711(t *testing.T) {
	h := wavHeader("pcmu", 16000)
	if len(h) != wavHeaderSize {
		t.Fatalf("header is %d bytes, want %d", len(h), wavHeaderSize)
	}
	if string(h[0:4]) != "RIFF" || string(h[8:12]) != "WAVE" || binary.LittleEndian.Uint16(h[20:22]) != 7 {
		t.Fatalf("not a mu-law WAV header: %x", h[:24])
	}
	if binary.LittleEndian.Uint32(h[24:28]) != 8000 || binary.LittleEndian.Uint32(h[54:58]) != 16000 {
		t.Fatal("sample rate or data length wrong")
	}
	if binary.LittleEndian.Uint16(wavHeader("pcma", 1)[20:22]) != 6 {
		t.Fatal("A-law must use format 6")
	}
}

func pkt(ts uint32, marker byte) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 0, Timestamp: ts}, Payload: bytes.Repeat([]byte{marker}, 160)}
}

func TestRecorderFillsGapsWithSilenceAndKeepsTracksApart(t *testing.T) {
	env := &recorderEnv{tmpDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := newRecorder("c1", recordingConfig{CallerUserID: 1, CalleeUserID: 2}, env)
	r.write(1, pkt(0, 0xA1))
	r.write(1, pkt(160, 0xA1))
	r.write(1, pkt(160*4, 0xA1)) // two packets (320 samples) lost in between
	r.write(2, pkt(5000, 0xB2))
	if len(r.tracks) != 2 {
		t.Fatalf("expected one track per speaker, got %d", len(r.tracks))
	}
	a := r.tracks[1]
	if a.bytes != 160*3+320 {
		t.Fatalf("caller track holds %d bytes, want %d (3 packets + 320 samples of silence)", a.bytes, 160*3+320)
	}
	_ = a.file.Sync()
	raw, _ := os.ReadFile(a.file.Name())
	if len(raw) != wavHeaderSize+int(a.bytes) {
		t.Fatalf("file size %d", len(raw))
	}
	if raw[wavHeaderSize+320] != 0xFF { // first gap sample is mu-law silence
		t.Fatalf("gap not filled with silence: %#x", raw[wavHeaderSize+320])
	}
	r.write(1, &rtp.Packet{Header: rtp.Header{PayloadType: 101}, Payload: []byte{1, 2}}) // DTMF is not audio
	if a.bytes != 160*3+320 {
		t.Fatal("telephone-event packet must not be recorded")
	}
}

func TestRecordingsAreSealedAndBoundToTheirObjectKey(t *testing.T) {
	box, _ := crypto.NewBox("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	plain := wavHeader("pcmu", 8000)
	sealed, _ := box.Seal(plain, []byte("call/caller.wav.enc"))
	if bytes.Contains(sealed, []byte("RIFF")) {
		t.Fatal("plain audio visible in the stored object")
	}
	if _, err := box.Open(sealed, []byte("other/caller.wav.enc")); err == nil {
		t.Fatal("a recording moved to another key must not decrypt")
	}
}
