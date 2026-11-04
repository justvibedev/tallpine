package chat

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *memorySideStore) UpsertSideMessage(_ context.Context, msg domain.SideMessage, generation string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[msg.SideID]
	if !ok || side.Generation != generation {
		return ErrSideClosed
	}
	for i := range m.messages[msg.SideID] {
		if m.messages[msg.SideID][i].ID != msg.ID {
			continue
		}
		previous := m.messages[msg.SideID][i]
		msg.Revision = previous.Revision
		if previous.Text != msg.Text || previous.Streaming != msg.Streaming {
			msg.Revision++
		}
		msg.Sequence = m.messages[msg.SideID][i].Sequence
		msg.CreatedAt = m.messages[msg.SideID][i].CreatedAt
		m.messages[msg.SideID][i] = msg
		return nil
	}
	msg.Sequence = int64(len(m.messages[msg.SideID]) + 1)
	msg.Revision = 1
	m.messages[msg.SideID] = append(m.messages[msg.SideID], msg)
	return nil
}
func (m *memorySideStore) SideMessages(_ context.Context, sideID string, turnIDs []string) ([]domain.SideMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := map[string]bool{}
	for _, id := range turnIDs {
		set[id] = true
	}
	out := []domain.SideMessage{}
	for _, msg := range m.messages[sideID] {
		if turnIDs == nil || set[msg.TurnID] {
			out = append(out, msg)
		}
	}
	return out, nil
}
func (m *memorySideStore) UpsertSideActivity(sideID, generation string, activity domain.SideActivity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[sideID]
	if !ok || side.Generation != generation {
		return ErrSideClosed
	}
	for i := range m.activities[sideID] {
		if m.activities[sideID][i].ID != activity.ID {
			continue
		}
		old := m.activities[sideID][i]
		if activity.Status == "running" && old.Status != "running" && old.Status != "pending" {
			activity.Status = old.Status
		}
		if activity.Kind == "system" && old.Kind != "" {
			activity.Kind = old.Kind
		}
		if len(activity.Detail) == 0 {
			activity.Detail = old.Detail
		}
		if activity.Summary == "" {
			activity.Summary = old.Summary
		}
		if activity.RequestID == "" {
			activity.RequestID = old.RequestID
		}
		if len(activity.Decisions) == 0 {
			activity.Decisions = old.Decisions
		}
		if activity.Input == nil {
			activity.Input = old.Input
		}
		activity.CreatedAt = old.CreatedAt
		m.activities[sideID][i] = activity
		return nil
	}
	m.activities[sideID] = append(m.activities[sideID], activity)
	return nil
}
func (m *memorySideStore) SideActivities(sideID string, turnIDs []string) []domain.SideActivity {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := map[string]bool{}
	for _, id := range turnIDs {
		set[id] = true
	}
	out := []domain.SideActivity{}
	for _, item := range m.activities[sideID] {
		if set[item.TurnID] {
			out = append(out, item)
		}
	}
	return out
}
func (m *memorySideStore) SideApproval(sideID, requestID string) (domain.SideActivity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.activities[sideID] {
		if item.RequestID == requestID && item.Kind == "approval" && item.Status == "pending" {
			return item, nil
		}
	}
	return domain.SideActivity{}, ErrSideClosed
}
func (m *memorySideStore) SideInput(sideID, requestID string) (domain.SideActivity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.activities[sideID] {
		if item.RequestID == requestID && item.Kind == "user_input" && item.Status == "pending" {
			return item, nil
		}
	}
	return domain.SideActivity{}, ErrSideClosed
}
func (m *memorySideStore) SetSideDraft(_ context.Context, sideID, runID, content string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[sideID]
	if !ok || side.AppRunID != runID {
		return ErrSideClosed
	}
	m.drafts[sideID] = content
	return nil
}
func (m *memorySideStore) SideDraft(_ context.Context, sideID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.drafts[sideID], nil
}
func (m *memorySideStore) SetSideReference(_ context.Context, sideID string, ref ports.ChatExcerptReference, selection, contextText string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateSide(sideID, "", func(s *domain.SideConversation) {
		s.SelectedText = selection
		s.ReferenceContext = contextText
		s.ReferencePending = true
		s.SourceMessageID = ref.MessageID
		s.SourceRevision = ref.Revision
		s.UpdatedAt = now
	})
}

func (m *memorySideStore) RenameSide(_ context.Context, id, label string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[id]
	if !ok || side.ClosedAt != nil {
		return ErrSideClosed
	}
	side.Label, side.ManualLabel, side.UpdatedAt = label, true, now
	m.sides[id] = side
	return nil
}
func (m *memorySideStore) CancelSideTurn(_ context.Context, sideID, turnID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.turns[sideID] {
		turn := &m.turns[sideID][i]
		if turn.ID == turnID {
			if turn.State != "queued" {
				return ErrSideQuestionInvalid
			}
			turn.State = "cancelled"
			turn.CompletedAt = &now
			return nil
		}
	}
	return ErrSideClosed
}

func sideReferenceContexts(refs []domain.SideReference) []string {
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = ref.Context
	}
	return out
}

// SetSideTurnProviderID records acceptance before streaming or deferred execution starts.
func (m *memorySideStore) SetSideTurnProviderID(_ context.Context, sideID, turnID, generation, providerID string) error {
	if providerID == "" {
		return ErrSidePolicyRecreate
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[sideID]
	if !ok || side.Generation != generation {
		return ErrSideClosed
	}
	for i := range m.turns[sideID] {
		turn := &m.turns[sideID][i]
		if turn.ID == turnID && turn.State == "running" {
			turn.ProviderTurnID = providerID
			if side.BoundaryTurnID == turnID {
				side.BoundaryState = "delivered"
				m.sides[sideID] = side
			}
			return nil
		}
	}
	return ErrSideClosed
}

// PrepareSideBoundary records the first delivery before provider I/O. An
// unacknowledged boundary is never replayed into a possibly accepted history.
func (m *memorySideStore) PrepareSideBoundary(_ context.Context, sideID, turnID, generation string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	side, ok := m.sides[sideID]
	if !ok || side.ClosedAt != nil || side.Generation != generation {
		return false, ErrSideClosed
	}
	if side.PolicyVersion != sidePolicyVersion {
		return false, ErrSidePolicyRecreate
	}
	switch side.BoundaryState {
	case "delivered":
		return false, nil
	case "":
		side.BoundaryTurnID, side.BoundaryState = turnID, "pending"
		m.sides[sideID] = side
		return true, nil
	default:
		return false, ErrSidePolicyRecreate
	}
}
