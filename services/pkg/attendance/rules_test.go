package attendance

import (
	"errors"
	"testing"
	"time"
)

func at(h, m int) time.Time { return time.Date(2026, 11, 12, h, m, 0, 0, time.UTC) }

func TestArrivalStatus(t *testing.T) {
	s := DefaultSchedule
	cases := []struct {
		h, m int
		want Arrival
	}{{7, 30, OnTime}, {8, 0, OnTime}, {8, 15, OnTime}, {8, 16, Late}, {11, 0, Late}}
	for _, c := range cases {
		if got := s.ArrivalStatus(at(c.h, c.m)); got != c.want {
			t.Errorf("%02d:%02d = %s, want %s", c.h, c.m, got, c.want)
		}
	}
}

func TestDepartureStatus(t *testing.T) {
	s := DefaultSchedule
	if _, err := s.DepartureStatus(at(16, 0), "  "); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("blank reason must be rejected, got %v", err)
	}
	if d, err := s.DepartureStatus(at(16, 0), "doctor"); err != nil || d != DepEarly {
		t.Fatalf("got %s %v", d, err)
	}
	if d, err := s.DepartureStatus(at(17, 0), ""); err != nil || d != DepNormal {
		t.Fatalf("got %s %v", d, err)
	}
}

func TestWorkDateUsesSiteTimezone(t *testing.T) {
	loc, _ := time.LoadLocation("Africa/Dar_es_Salaam")    // UTC+3
	utc := time.Date(2026, 11, 12, 22, 30, 0, 0, time.UTC) // 01:30 on the 13th locally
	if got := WorkDate(utc, loc).Day(); got != 13 {
		t.Fatalf("day = %d, want 13", got)
	}
}

func TestMinutes(t *testing.T) {
	if Minutes(at(8, 0), at(17, 30)) != 570 || Minutes(at(9, 0), at(8, 0)) != 0 {
		t.Fatal("unexpected duration")
	}
}

func TestIPAllowed(t *testing.T) {
	ranges := []string{"10.0.0.0/16", "bad", "192.168.1.0/24"}
	for ip, want := range map[string]bool{
		"10.0.5.9": true, "192.168.1.200": true, "10.1.0.1": false, "8.8.8.8": false, "": false, "nonsense": false,
	} {
		if IPAllowed(ip, ranges) != want {
			t.Errorf("%q expected %v", ip, want)
		}
	}
}
