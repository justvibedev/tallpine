package chat

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func newMemorySideStore() *memorySideStore {
	return &memorySideStore{hostCleanup: map[string]domain.SideConversation{}, hostStopped: map[string]bool{}, closed: map[string]bool{}, sides: map[string]domain.SideConversation{}, turns: map[string][]domain.SideTurn{}, messages: map[string][]domain.SideMessage{}, activities: map[string][]domain.SideActivity{}, drafts: map[string]string{}, cleanup: map[string]domain.SideProviderCleanup{}}
}

func (m *memorySideStore) ClaimSideLaunch(_ context.Context, runID string, _ time.Time) ([]domain.SideConversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if runID == "" {
		return nil, ErrSideLaunchUnclaimed
	}
	if m.runID == runID {
		return nil, nil
	}
	retired := make([]domain.SideConversation, 0, len(m.sides))
	for _, side := range m.sides {
		retired = append(retired, side)
	}
	m.runID = runID
	m.closed = map[string]bool{}
	m.sides = map[string]domain.SideConversation{}
	m.turns = map[string][]domain.SideTurn{}
	m.messages = map[string][]domain.SideMessage{}
	m.activities = map[string][]domain.SideActivity{}
	m.drafts = map[string]string{}
	return retired, nil
}
func (m *memorySideStore) RecoverInterruptedSideTurns(_ context.Context, runID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runID != runID {
		return nil
	}
	for id, turns := range m.turns {
		for i := range turns {
			if turns[i].State == "running" {
				turns[i].State = "failed"
				turns[i].ErrorMessage = "Daemon restarted; retry this turn."
				turns[i].CompletedAt = &now
			}
		}
		m.turns[id] = turns
	}
	return nil
}
func (m *memorySideStore) CreateSideConversation(_ context.Context, side domain.SideConversation) (domain.SideConversation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if side.AppRunID == "" || side.AppRunID != m.runID {
		return domain.SideConversation{}, false, ErrSideLaunchUnclaimed
	}
	for _, current := range m.sides {
		if current.MainConversationID == side.MainConversationID && current.ClosedAt == nil && ((side.CreateKey != "" && current.CreateKey == side.CreateKey) || (!side.ForceNew && current.AnchorTurnID == side.AnchorTurnID)) {
			return current, false, nil
		}
	}
	largest, open := 1, false
	for _, current := range m.sides {
		if current.SessionID == side.SessionID && current.ClosedAt == nil {
			open = true
			largest = max(largest, sideChatNumber(current.Label))
		}
	}
	side.Label = "Side Chat"
	if open {
		if largest >= 99 {
			return domain.SideConversation{}, false, ErrSideNameLimit
		}
		side.Label = fmt.Sprintf("Side Chat %d", largest+1)
	}
	m.sides[side.ID] = side
	return side, true, nil
}

func sideChatNumber(label string) int {
	if label == "Side Chat" {
		return 1
	}
	if !strings.HasPrefix(label, "Side Chat ") {
		return 0
	}
	number, err := strconv.Atoi(strings.TrimPrefix(label, "Side Chat "))
	if err != nil || number < 1 || number > 99 {
		return 0
	}
	return number
}
func (m *memorySideStore) SideConversation(_ context.Context, id string) (domain.SideConversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[id]
	if !ok || side.ClosedAt != nil {
		return side, ErrSideClosed
	}
	return side, nil
}
func (m *memorySideStore) ListSideConversations(_ context.Context, session domain.SessionID, runID string) ([]domain.SideConversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []domain.SideConversation{}
	for _, side := range m.sides {
		if side.SessionID == session && side.AppRunID == runID && side.ClosedAt == nil {
			out = append(out, side)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}
func (m *memorySideStore) ListOpenSidesForRun(_ context.Context, runID string) ([]domain.SideConversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []domain.SideConversation{}
	for _, side := range m.sides {
		if side.AppRunID == runID && side.ClosedAt == nil {
			out = append(out, side)
		}
	}
	return out, nil
}
func (m *memorySideStore) updateSide(id, generation string, fn func(*domain.SideConversation)) error {
	side, ok := m.sides[id]
	if !ok || side.ClosedAt != nil || (generation != "" && side.Generation != generation) {
		return ErrSideClosed
	}
	fn(&side)
	m.sides[id] = side
	return nil
}
func (m *memorySideStore) SetSideRecovering(_ context.Context, id, generation string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateSide(id, generation, func(side *domain.SideConversation) {
		side.State = "recovering"
		side.ErrorMessage = ""
		side.RecreateRequired = false
		side.UpdatedAt = now
	})
}
func (m *memorySideStore) SetSideReady(_ context.Context, id, generation, providerID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateSide(id, generation, func(s *domain.SideConversation) {
		s.State = "ready"
		s.ErrorMessage = ""
		s.RecreateRequired = false
		s.ProviderForkID = providerID
		s.UpdatedAt = now
	})
}
func (m *memorySideStore) SetSideReconstructed(_ context.Context, id, generation string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateSide(id, generation, func(side *domain.SideConversation) {
		side.ContextMode = "reconstructed"
		side.UpdatedAt = now
	})
}
func (m *memorySideStore) RegisterSideFork(_ context.Context, id, generation, providerID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateSide(id, generation, func(s *domain.SideConversation) {
		s.ProviderForkID = providerID
		s.UpdatedAt = now
		m.cleanup[providerID] = domain.SideProviderCleanup{ForkID: providerID, SessionID: s.SessionID, Harness: s.Harness}
	})
}
func (m *memorySideStore) SetSideFailed(_ context.Context, id, generation, message string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateSide(id, generation, func(s *domain.SideConversation) {
		if s.BoundaryState == "pending" {
			s.BoundaryState = "uncertain"
		}
		s.State = "failed"
		s.ErrorMessage = message
		s.RecreateRequired = strings.HasPrefix(message, ErrSidePolicyRecreate.Error())
		s.UpdatedAt = now
		if s.RecreateRequired && s.ProviderForkID != "" {
			m.hostCleanup[id] = *s
		}
	})
}
func (m *memorySideStore) CloseSideConversation(_ context.Context, id string, now time.Time) (domain.SideConversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[id]
	if !ok || side.ClosedAt != nil {
		return side, ErrSideClosed
	}
	m.closed[id] = true
	m.hostCleanup[id] = side
	side.ClosedAt = &now
	side.Generation = ""
	delete(m.sides, id)
	delete(m.turns, id)
	delete(m.messages, id)
	delete(m.activities, id)
	delete(m.drafts, id)
	return side, nil
}
func (m *memorySideStore) SideHostCleanupPending(context.Context) []domain.SideConversation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.SideConversation, 0, len(m.hostCleanup))
	for _, side := range m.hostCleanup {
		if !m.hostStopped[side.ID] {
			out = append(out, side)
		}
	}
	return out
}
func (m *memorySideStore) CompleteSideHostCleanup(_ context.Context, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hostStopped[id] = true
	if side, ok := m.hostCleanup[id]; ok && side.ProviderForkID == "" {
		delete(m.hostCleanup, id)
		delete(m.hostStopped, id)
	}
}
func (m *memorySideStore) CompleteSideProviderCleanup(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for sideID, side := range m.hostCleanup {
		if side.ProviderForkID == id {
			delete(m.hostCleanup, sideID)
			delete(m.hostStopped, sideID)
		}
	}
	for sideID, side := range m.sides {
		if side.ProviderForkID == id && side.State == "failed" && side.RecreateRequired {
			side.ProviderForkID = ""
			m.sides[sideID] = side
		}
	}
	delete(m.cleanup, id)
	return nil
}
func (m *memorySideStore) SideProviderCleanupPending(_ context.Context) ([]domain.SideProviderCleanup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	openForks := make(map[string]bool, len(m.sides))
	for _, side := range m.sides {
		if side.ClosedAt == nil && side.ProviderForkID != "" && (side.State != "failed" || !side.RecreateRequired) {
			openForks[side.ProviderForkID] = true
		}
	}
	out := make([]domain.SideProviderCleanup, 0, len(m.cleanup))
	for _, item := range m.cleanup {
		hostPending := false
		for _, side := range m.hostCleanup {
			if side.ProviderForkID == item.ForkID && !m.hostStopped[side.ID] {
				hostPending = true
			}
		}
		if !openForks[item.ForkID] && !hostPending {
			out = append(out, item)
		}
	}
	return out, nil
}
