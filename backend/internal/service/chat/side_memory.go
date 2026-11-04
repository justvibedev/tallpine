package chat

import (
	"encoding/json"
	"errors"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
)

var (
	// ErrSideClosed indicates that a side is closed or has a stale generation.
	ErrSideClosed = errors.New("side chat is closed")
	// ErrSideLaunchUnclaimed indicates that no desktop launch owns the side state.
	ErrSideLaunchUnclaimed = errors.New("desktop launch has not claimed side chats")
	// ErrSideIdempotencyConflict indicates that a request key was reused for another question.
	ErrSideIdempotencyConflict = errors.New("side request key belongs to another question")
	// ErrSideNameLimit indicates that the highest open default name is already 99.
	ErrSideNameLimit = errors.New("side chat numbering has reached 99; close or rename the highest-numbered tab before creating another")
)

// memorySideStore is deliberately process-local. Electron owns recovery across
// supervised daemon restarts; Tallpine never writes side transcripts or drafts to DB.
type memorySideStore struct {
	mu          sync.Mutex
	runID       string
	closed      map[string]bool
	sides       map[string]domain.SideConversation
	turns       map[string][]domain.SideTurn
	messages    map[string][]domain.SideMessage
	activities  map[string][]domain.SideActivity
	drafts      map[string]string
	hostCleanup map[string]domain.SideConversation
	hostStopped map[string]bool
	cleanup     map[string]domain.SideProviderCleanup
}

// SideRecoveryRecord travels only between the daemon and Electron main process.
// Electron holds it in RAM for this desktop launch and never writes it to disk.
type SideRecoveryRecord struct {
	CleanupOnly         bool                    `json:"cleanupOnly,omitempty"`
	HostStopped         bool                    `json:"hostStopped,omitempty"`
	PolicyVersion       int                     `json:"policyVersion"`
	BoundaryTurnID      string                  `json:"boundaryTurnId,omitempty"`
	BoundaryState       string                  `json:"boundaryState,omitempty"`
	MessageProviderIDs  map[string]string       `json:"messageProviderIds,omitempty"`
	ActivityProviderIDs map[string]string       `json:"activityProviderIds,omitempty"`
	DecisionData        map[string][][]byte     `json:"decisionData,omitempty"`
	SourceProviderID    string                  `json:"sourceProviderId"`
	LaunchConfig        json.RawMessage         `json:"launchConfig"`
	CreateKey           string                  `json:"createKey"`
	Side                domain.SideConversation `json:"side"`
	ProviderHostID      string                  `json:"providerHostId"`
	ProviderForkID      string                  `json:"providerForkId"`
	NativeAnchorID      string                  `json:"nativeAnchorId"`
	Harness             domain.AgentHarness     `json:"harness"`
	Generation          string                  `json:"generation"`
	ReferenceContext    string                  `json:"referenceContext"`
	ReferencePending    bool                    `json:"referencePending"`
	SeedHistory         string                  `json:"seedHistory"`
	Turns               []SideRecoveryTurn      `json:"turns"`
	Messages            []domain.SideMessage    `json:"messages"`
	Activities          []domain.SideActivity   `json:"activities"`
	Draft               string                  `json:"draft"`
}

// SideRecoveryTurn pairs a recoverable side turn with its structured content.
type SideRecoveryTurn struct {
	DispatchAttempt   int                  `json:"dispatchAttempt,omitempty"`
	ProviderTurnID    string               `json:"providerTurnId,omitempty"`
	ReferenceContexts []string             `json:"referenceContexts"`
	Turn              domain.SideTurn      `json:"turn"`
	Content           []domain.SideContent `json:"content,omitempty"`
}
