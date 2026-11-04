package domain

import (
	"errors"
	"strings"
	"time"
)

// MaxOrchestratorIntentLength bounds one intent's text in bytes.
const MaxOrchestratorIntentLength = 2 << 10

// ErrInvalidOrchestratorIntent reports empty or oversized intent text.
var ErrInvalidOrchestratorIntent = errors.New("invalid orchestrator intent")

// ErrOrchestratorIntentNotFound reports an unknown or already-cleared intent.
var ErrOrchestratorIntentNotFound = errors.New("orchestrator intent not found")

// OrchestratorIntent is one owner intent or standing decision, kept in the
// daemon so it survives compaction and handoff (B374). DoneAt is nil while open.
type OrchestratorIntent struct {
	ID        string
	ProjectID ProjectID
	Text      string
	CreatedBy string
	CreatedAt time.Time
	DoneAt    *time.Time
}

// NormalizeOrchestratorIntentText trims text and validates its length.
func NormalizeOrchestratorIntentText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > MaxOrchestratorIntentLength {
		return "", ErrInvalidOrchestratorIntent
	}
	return text, nil
}
