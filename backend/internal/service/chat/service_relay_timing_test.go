package chat

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLogSlowRelayWarnsOnlyOverThreshold(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := &Service{log: slog.New(slog.NewTextHandler(&buf, nil))}
	s.logSlowRelay("s1", time.Second, time.Second, nil)
	if buf.Len() != 0 {
		t.Fatalf("fast relay logged: %s", buf.String())
	}
	s.logSlowRelay("s1", 4*time.Second, 2*time.Second, nil)
	out := buf.String()
	if !strings.Contains(out, "slow chat send") || !strings.Contains(out, "gateWait=4s") || !strings.Contains(out, "dispatch=2s") {
		t.Fatalf("slow relay log = %q", out)
	}
}
