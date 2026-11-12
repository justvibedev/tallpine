package store_test

import (
	"context"
	"testing"
	"time"
)

// The peer check switch defaults to "default", round-trips "off", and the
// column refuses any other value (the documented sqlite3 recovery route
// cannot store garbage the daemon would misread).
func TestPeerCheckSwitchRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	row, err := s.GetAppSettings(ctx)
	if err != nil || row.PeerCheck != "default" {
		t.Fatalf("fresh row = %q, %v; want default", row.PeerCheck, err)
	}
	if err := s.SetPeerCheck(ctx, "off", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if row, _ := s.GetAppSettings(ctx); row.PeerCheck != "off" {
		t.Fatalf("after off = %q", row.PeerCheck)
	}
	if err := s.SetPeerCheck(ctx, "disabled", time.Now().UTC()); err == nil {
		t.Fatal("the column accepted a value outside default/off")
	}
}
