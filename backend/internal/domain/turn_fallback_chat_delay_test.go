package domain

import (
	"testing"
	"time"
)

func TestChatTurnFallbackDelayIsShortAndBounded(t *testing.T) {
	if got := ChatTurnFallbackDelay(); got >= TurnFallbackDelay || got > 5*time.Second {
		t.Fatalf("chat delay = %v, want a few seconds below %v", got, TurnFallbackDelay)
	}
	old := TurnFallbackDelay
	TurnFallbackDelay = time.Second
	defer func() { TurnFallbackDelay = old }()
	if got := ChatTurnFallbackDelay(); got != time.Second {
		t.Fatalf("chat delay = %v, want capped at %v", got, time.Second)
	}
}
