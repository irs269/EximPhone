package sdpmini

import "testing"

const phoneOffer = "v=0\r\no=- 1 1 IN IP4 10.0.0.9\r\ns=-\r\nc=IN IP4 10.0.0.9\r\nt=0 0\r\n" +
	"m=audio 11780 RTP/AVP 0 8 101\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:101 telephone-event/8000\r\na=sendrecv\r\n"

func TestParsePhoneOffer(t *testing.T) {
	a, err := Parse([]byte(phoneOffer))
	if err != nil {
		t.Fatal(err)
	}
	if a.Addr != "10.0.0.9" || a.Port != 11780 || !a.Has(PTPCMU) || !a.Has(PTPCMA) || a.DTMFPT != 101 {
		t.Fatalf("unexpected %+v", a)
	}
}

func TestMediaLevelConnectionOverridesSession(t *testing.T) {
	sdp := "v=0\r\nc=IN IP4 1.1.1.1\r\nm=audio 4000 RTP/AVP 0\r\nc=IN IP4 2.2.2.2\r\n"
	a, err := Parse([]byte(sdp))
	if err != nil || a.Addr != "2.2.2.2" {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	for _, s := range []string{"", "v=0\r\n", "v=0\r\nm=video 4000 RTP/AVP 96\r\nc=IN IP4 1.2.3.4\r\n", "v=0\r\nm=audio 4000 RTP/AVP 0\r\n"} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("%q must be rejected", s)
		}
	}
}

func TestBuildRoundTrip(t *testing.T) {
	a, err := Parse(Build("192.0.2.10", 10123, 42))
	if err != nil {
		t.Fatal(err)
	}
	if a.Addr != "192.0.2.10" || a.Port != 10123 || !a.Has(PTPCMU) || a.Has(PTPCMA) || a.DTMFPT != 101 {
		t.Fatalf("unexpected %+v", a)
	}
}
