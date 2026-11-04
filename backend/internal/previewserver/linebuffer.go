package previewserver

import (
	"strings"
	"sync"
)

type lineBuffer struct {
	mu      sync.Mutex
	max     int
	lines   []string
	partial string
}

func newLineBuffer(capacity int) *lineBuffer {
	return &lineBuffer{max: capacity}
}

func (b *lineBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text := b.partial + string(data)
	parts := strings.Split(text, "\n")
	b.partial = parts[len(parts)-1]
	if len(b.partial) > maxBufferedPartialBytes {
		b.partial = b.partial[len(b.partial)-maxBufferedPartialBytes:]
	}
	for _, line := range parts[:len(parts)-1] {
		b.appendLocked(strings.TrimSuffix(line, "\r"))
	}
	return len(data), nil
}

func (b *lineBuffer) Last(limit int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := append([]string{}, b.lines...)
	if b.partial != "" {
		lines = append(lines, b.partial)
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}

func (b *lineBuffer) appendLocked(line string) {
	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = append([]string{}, b.lines[len(b.lines)-b.max:]...)
	}
}
