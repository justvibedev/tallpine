package cli

import (
	"testing"
	"time"
)

func TestParseWakeAt(t *testing.T) {
	loc := time.FixedZone("EET", 2*3600)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want string
		bad  bool
	}{
		{in: ""},
		{in: "90m", want: "2026-11-13T13:30:00Z"},
		{in: "2026-11-14T06:20:00Z", want: "2026-11-14T06:20:00Z"},
		{in: "2026-11-14 06:20", want: "2026-11-14T04:20:00Z"},
		{in: "2026-11-13 08:00", bad: true},
		{in: "soon", bad: true},
	}
	for _, c := range cases {
		got, err := parseWakeAt(c.in, now, loc)
		if c.bad {
			if err == nil {
				t.Errorf("%q: want error, got %v", c.in, got)
			}
			continue
		}
		switch {
		case err != nil:
			t.Errorf("%q: %v", c.in, err)
		case c.want == "" && got != nil:
			t.Errorf("%q: want nil, got %v", c.in, got)
		case c.want != "" && (got == nil || got.Format(time.RFC3339) != c.want):
			t.Errorf("%q: got %v want %s", c.in, got, c.want)
		}
	}
}
