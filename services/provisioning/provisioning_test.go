package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func fake(vendor string) lookup {
	return func(_ context.Context, mac string) (*phone, error) {
		if mac != "001565aabbcc" {
			return nil, nil
		}
		return &phone{Vendor: vendor, Extension: "2001", DisplayName: "Amina Said", SIPUser: "2001", Password: "s3cr3t", Server: "10.0.0.5", Port: 5060}, nil
	}
}

func serve(find lookup) *httptest.Server {
	r := chi.NewRouter()
	r.Get("/{file}", handler(find, slog.New(slog.NewTextHandler(io.Discard, nil))))
	return httptest.NewServer(r)
}

func get(t *testing.T, url string) (int, string, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header.Get("Content-Type")
}

func TestYealinkByMAC(t *testing.T) {
	srv := serve(fake("yealink"))
	defer srv.Close()
	code, body, _ := get(t, srv.URL+"/001565aabbcc.cfg")
	if code != 200 || !strings.Contains(body, "account.1.user_name = 2001") || !strings.Contains(body, "account.1.password = s3cr3t") ||
		!strings.Contains(body, "account.1.sip_server.1.address = 10.0.0.5") || !strings.Contains(body, "account.1.codec.pcmu.priority = 1") {
		t.Fatalf("%d\n%s", code, body)
	}
}

func TestGrandstreamXML(t *testing.T) {
	srv := serve(fake("grandstream"))
	defer srv.Close()
	code, body, ct := get(t, srv.URL+"/cfg001565aabbcc.xml")
	if code != 200 || !strings.Contains(ct, "xml") || !strings.Contains(body, "<P35>2001</P35>") || !strings.Contains(body, "<P47>10.0.0.5:5060</P47>") {
		t.Fatalf("%d %s\n%s", code, ct, body)
	}
}

func TestFanvil(t *testing.T) {
	srv := serve(fake("fanvil"))
	defer srv.Close()
	code, body, _ := get(t, srv.URL+"/001565aabbcc.cfg")
	if code != 200 || !strings.Contains(body, "SIP1 Register User :2001") {
		t.Fatalf("%d\n%s", code, body)
	}
}

func TestUnknownAndMalformedNames(t *testing.T) {
	srv := serve(fake("yealink"))
	defer srv.Close()
	for _, p := range []string{"/ffffffffffff.cfg", "/y000000000000.cfg", "/..%2fetc%2fpasswd", "/001565AABBCC.cfg", "/001565aabbcc.txt"} {
		if code, _, _ := get(t, srv.URL+p); code != 404 {
			t.Errorf("%s -> %d, want 404", p, code)
		}
	}
}

func TestNoConfigInjection(t *testing.T) {
	p := phone{Vendor: "yealink", Extension: "2001", DisplayName: "Eve\r\naccount.1.password = hacked", SIPUser: "2001", Password: "x", Server: "h", Port: 5060}
	out, err := render(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\naccount.1.password = hacked") {
		t.Fatalf("newline injection: %s", out)
	}
	p.Vendor, p.DisplayName = "grandstream", `<x>"&`
	out, _ = render(p)
	if strings.Contains(out, "<x>") {
		t.Fatalf("xml not escaped: %s", out)
	}
}

func TestRateLimit(t *testing.T) {
	srv := serve(fake("yealink"))
	defer srv.Close()
	last := 0
	for i := 0; i < 40; i++ {
		last, _, _ = get(t, srv.URL+"/001565aabbcc.cfg")
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("last status %d, want 429", last)
	}
}
