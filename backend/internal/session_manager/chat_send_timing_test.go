package sessionmanager

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLogSlowChatSendReportsPhasesOnlyWhenSlow(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	base := time.Unix(1000, 0)
	end := base.Add(time.Second)
	m := &Manager{logger: slog.New(slog.NewTextHandler(&buf, nil)), clock: func() time.Time { return end }}
	m.logSlowChatSend("s1", base, base.Add(500*time.Millisecond))
	if buf.Len() != 0 {
		t.Fatalf("fast send logged: %s", buf.String())
	}
	end = base.Add(7 * time.Second)
	m.logSlowChatSend("s1", base, base.Add(4*time.Second))
	out := buf.String()
	for _, want := range []string{"slow chat send (manager)", "total=7s", "relay=4s", "delivery=3s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("slow send log = %q, missing %q", out, want)
		}
	}
}
