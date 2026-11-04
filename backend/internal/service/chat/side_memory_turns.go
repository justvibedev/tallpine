package chat

import (
	"context"
	"reflect"
	"sort"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func (m *memorySideStore) ReserveSideTurn(_ context.Context, turn domain.SideTurn, runID string) (domain.SideTurn, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[turn.SideID]
	if !ok || side.ClosedAt != nil || side.AppRunID != runID {
		return domain.SideTurn{}, false, ErrSideClosed
	}
	for _, existing := range m.turns[turn.SideID] {
		if existing.ClientMessageID == turn.ClientMessageID {
			if existing.Text != turn.Text || !reflect.DeepEqual(existing.Content, turn.Content) || !reflect.DeepEqual(existing.References, turn.References) {
				return domain.SideTurn{}, false, ErrSideIdempotencyConflict
			}
			return existing, false, nil
		}
	}
	if side.State != "ready" && side.State != "opening" {
		return domain.SideTurn{}, false, ErrSideNotReady
	}
	side.ReferencePending = false
	m.sides[side.ID] = side
	turn.State = "queued"
	m.turns[turn.SideID] = append(m.turns[turn.SideID], turn)
	m.messages[turn.SideID] = append(m.messages[turn.SideID], domain.SideMessage{
		ID: turn.ID + "-user", SideID: turn.SideID, TurnID: turn.ID, Role: "user", Text: turn.Text,
		Sequence: int64(len(m.messages[turn.SideID]) + 1), CreatedAt: turn.CreatedAt, UpdatedAt: turn.CreatedAt,
	})
	return turn, true, nil
}
func (m *memorySideStore) ClaimNextSideTurn(_ context.Context, runID string, excluded map[string]bool, now time.Time) (domain.SideTurn, domain.SideConversation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var chosen domain.SideTurn
	var side domain.SideConversation
	found := false
	for id, turns := range m.turns {
		current, ok := m.sides[id]
		if !ok || current.AppRunID != runID || current.State != "ready" || excluded[id] {
			continue
		}
		busy := false
		for _, turn := range turns {
			if turn.State == "running" {
				busy = true
				break
			}
		}
		if busy {
			continue
		}
		for _, turn := range turns {
			if turn.State == "queued" && (!found || turn.CreatedAt.Before(chosen.CreatedAt)) {
				chosen, side, found = turn, current, true
			}
		}
	}
	if !found {
		return chosen, side, false, nil
	}
	for i := range m.turns[side.ID] {
		if m.turns[side.ID][i].ID == chosen.ID {
			m.turns[side.ID][i].State = "running"
			m.turns[side.ID][i].StartedAt = &now
			chosen = m.turns[side.ID][i]
			break
		}
	}
	return chosen, side, true, nil
}
func (m *memorySideStore) SettleSideTurn(_ context.Context, sideID, turnID, generation, state, providerID, message string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[sideID]
	if !ok || side.Generation != generation {
		return ErrSideClosed
	}
	for i := range m.turns[sideID] {
		if m.turns[sideID][i].ID != turnID {
			continue
		}
		turn := &m.turns[sideID][i]
		turn.State = state
		turn.ProviderTurnID = providerID
		turn.ErrorMessage = message
		turn.CompletedAt = &now
		return nil
	}
	return ErrSideClosed
}
func (m *memorySideStore) RequeueSideTurn(_ context.Context, sideID, turnID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.turns[sideID] {
		if m.turns[sideID][i].ID == turnID {
			m.turns[sideID][i].State = "queued"
			return nil
		}
	}
	return ErrSideClosed
}
func (m *memorySideStore) RetrySideTurn(_ context.Context, sideID, turnID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.turns[sideID] {
		turn := &m.turns[sideID][i]
		if turn.ID != turnID || turn.State != "failed" {
			continue
		}
		if turn.ProviderTurnID != "" {
			turn.RetryOfTurnID = turn.ID
		}
		turn.DispatchAttempt++
		turn.ProviderTurnID = ""
		turn.State = "queued"
		turn.ErrorMessage = ""
		turn.StartedAt, turn.CompletedAt = nil, nil
		return nil
	}
	return ErrSideClosed
}
func (m *memorySideStore) UpdateSideSettings(_ context.Context, id, model, effort string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateSide(id, "", func(s *domain.SideConversation) { s.Model = model; s.Effort = effort; s.UpdatedAt = now })
}
func (m *memorySideStore) EditQueuedSideTurn(_ context.Context, sideID, turnID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.turns[sideID] {
		if m.turns[sideID][i].ID == turnID && m.turns[sideID][i].State == "queued" {
			m.turns[sideID][i].Text = text
			for j := range m.messages[sideID] {
				if m.messages[sideID][j].TurnID == turnID && m.messages[sideID][j].Role == "user" {
					m.messages[sideID][j].Text = text
				}
			}
			return nil
		}
	}
	return ErrSideClosed
}
func (m *memorySideStore) SideTurn(_ context.Context, sideID, turnID string) (domain.SideTurn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, turn := range m.turns[sideID] {
		if turn.ID == turnID {
			return turn, nil
		}
	}
	return domain.SideTurn{}, ErrSideClosed
}
func (m *memorySideStore) SideTurns(_ context.Context, sideID string, before time.Time, limit int) ([]domain.SideTurn, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	turns := append([]domain.SideTurn{}, m.turns[sideID]...)
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].CreatedAt.Before(turns[j].CreatedAt) })
	if !before.IsZero() {
		filtered := turns[:0]
		for _, turn := range turns {
			if turn.CreatedAt.Before(before) {
				filtered = append(filtered, turn)
			}
		}
		turns = filtered
	}
	if limit <= 0 || limit > len(turns) {
		limit = len(turns)
	}
	more := len(turns) > limit
	return append([]domain.SideTurn{}, turns[len(turns)-limit:]...), more, nil
}
