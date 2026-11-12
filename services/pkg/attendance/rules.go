// Package attendance holds the pure business rules for clock-in/out. No I/O here.
package attendance

import (
	"errors"
	"net"
	"time"
)

type Arrival string
type Departure string

const (
	OnTime Arrival = "on_time"
	Late   Arrival = "late"

	DepNormal     Departure = "normal"
	DepEarly      Departure = "early"
	DepNotClocked Departure = "not_clocked"
)

// ErrReasonRequired is returned when leaving early without a justification.
var ErrReasonRequired = errors.New("early departure requires a reason")

// Schedule is a site's working-hours rule, expressed as local times of day.
type Schedule struct {
	Start     time.Duration // offset from local midnight, e.g. 8h
	End       time.Duration
	Tolerance time.Duration
}

// DefaultSchedule: 08:00 start, 15 min tolerance, 17:00 end.
var DefaultSchedule = Schedule{Start: 8 * time.Hour, End: 17 * time.Hour, Tolerance: 15 * time.Minute}

func sinceMidnight(t time.Time) time.Duration {
	h, m, s := t.Clock()
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
}

// ArrivalStatus classifies a clock-in. t must already be in the site's timezone.
// Arriving exactly at start+tolerance is still on time.
func (s Schedule) ArrivalStatus(t time.Time) Arrival {
	if sinceMidnight(t) > s.Start+s.Tolerance {
		return Late
	}
	return OnTime
}

// DepartureStatus classifies a clock-out. Before End a non-blank reason is mandatory.
func (s Schedule) DepartureStatus(t time.Time, reason string) (Departure, error) {
	if sinceMidnight(t) < s.End {
		if len(trimSpace(reason)) == 0 {
			return "", ErrReasonRequired
		}
		return DepEarly, nil
	}
	return DepNormal, nil
}

// WorkDate returns the local calendar date (as midnight UTC) for t at a site.
func WorkDate(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Minutes is the whole number of minutes between in and out (never negative).
func Minutes(in, out time.Time) int {
	if out.Before(in) {
		return 0
	}
	return int(out.Sub(in) / time.Minute)
}

// IPAllowed reports whether ip falls in any of the CIDR ranges. Unparseable input is denied.
func IPAllowed(ip string, cidrs []string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil && n.Contains(parsed) {
			return true
		}
	}
	return false
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}
