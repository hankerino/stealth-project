package main

import (
	"net/url"
	"testing"
	"time"
)

func TestParsePagination(t *testing.T) {
	lim, off, err := parsePagination(url.Values{})
	if err != nil || lim != defaultLimit || off != 0 {
		t.Fatalf("defaults: lim=%d off=%d err=%v", lim, off, err)
	}
	lim, off, err = parsePagination(url.Values{"limit": {"50"}, "offset": {"10"}})
	if err != nil || lim != 50 || off != 10 {
		t.Fatalf("explicit: lim=%d off=%d err=%v", lim, off, err)
	}
	if lim, _, _ := parsePagination(url.Values{"limit": {"99999"}}); lim != maxLimit {
		t.Fatalf("cap: lim=%d want %d", lim, maxLimit)
	}
	for _, bad := range []url.Values{{"limit": {"0"}}, {"limit": {"-1"}}, {"limit": {"x"}}, {"offset": {"-3"}}} {
		if _, _, err := parsePagination(bad); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}
}

func TestParseIntervalSeconds(t *testing.T) {
	cases := map[string]int{"": defaultInterval, "30s": 30, "5m": 300, "1h": 3600, "2h": 7200, "1d": 86400}
	for in, want := range cases {
		v := url.Values{}
		if in != "" {
			v.Set("interval", in)
		}
		got, err := parseIntervalSeconds(v)
		if err != nil || got != want {
			t.Fatalf("interval %q => %d err=%v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"5", "1x", "hm", "-1m", "0h"} {
		if _, err := parseIntervalSeconds(url.Values{"interval": {bad}}); err == nil {
			t.Fatalf("expected error for interval %q", bad)
		}
	}
}

func TestParseTimeRange(t *testing.T) {
	from, to, err := parseTimeRange(url.Values{"from": {"2026-01-01"}, "to": {"2026-01-02"}})
	if err != nil {
		t.Fatal(err)
	}
	if from.Format("2006-01-02") != "2026-01-01" || to.Format("2006-01-02") != "2026-01-02" {
		t.Fatalf("range parsed wrong: %s..%s", from, to)
	}
	// default: from = to - 24h
	f2, t2, err := parseTimeRange(url.Values{})
	if err != nil || t2.Sub(f2) != defaultLookback {
		t.Fatalf("default lookback wrong: %s", t2.Sub(f2))
	}
	// RFC3339
	if _, _, err := parseTimeRange(url.Values{"from": {"2026-01-01T00:00:00Z"}, "to": {"2026-01-01T06:00:00Z"}}); err != nil {
		t.Fatalf("rfc3339: %v", err)
	}
	// from >= to rejected
	if _, _, err := parseTimeRange(url.Values{"from": {"2026-01-02"}, "to": {"2026-01-01"}}); err == nil {
		t.Fatal("expected from<to error")
	}
	// bad format
	if _, _, err := parseTimeRange(url.Values{"to": {"nope"}}); err == nil {
		t.Fatal("expected bad-time error")
	}
}

func TestParseTimeFormats(t *testing.T) {
	if _, err := parseTime("2026-08-15"); err != nil {
		t.Fatal(err)
	}
	if got, err := parseTime("2026-08-15T12:00:00Z"); err != nil || got.Hour() != 12 {
		t.Fatalf("rfc3339 parse: %v %v", got, err)
	}
	if _, err := parseTime("15/08/2026"); err == nil {
		t.Fatal("expected error")
	}
}

var _ = time.Now
