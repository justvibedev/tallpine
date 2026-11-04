package chat

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func (m *memorySideStore) export(runID string) []SideRecoveryRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []SideRecoveryRecord{}
	for id, side := range m.sides {
		if side.AppRunID != runID || side.ClosedAt != nil {
			continue
		}
		turns := make([]SideRecoveryTurn, 0, len(m.turns[id]))
		for _, turn := range m.turns[id] {
			turns = append(turns, SideRecoveryTurn{Turn: turn, DispatchAttempt: turn.DispatchAttempt, ProviderTurnID: turn.ProviderTurnID, Content: turn.Content, ReferenceContexts: sideReferenceContexts(turn.References)})
		}
		messageIDs, activityIDs, decisions := map[string]string{}, map[string]string{}, map[string][][]byte{}
		for _, message := range m.messages[id] {
			messageIDs[message.ID] = message.ProviderItemID
		}
		for _, activity := range m.activities[id] {
			activityIDs[activity.ID] = activity.ProviderItemID
			for _, decision := range activity.Decisions {
				decisions[activity.ID] = append(decisions[activity.ID], decision.Raw)
			}
		}
		out = append(out, SideRecoveryRecord{PolicyVersion: side.PolicyVersion, BoundaryTurnID: side.BoundaryTurnID, BoundaryState: side.BoundaryState, MessageProviderIDs: messageIDs, ActivityProviderIDs: activityIDs, DecisionData: decisions, Side: side, SourceProviderID: side.SourceProviderID, LaunchConfig: side.LaunchConfig, CreateKey: side.CreateKey, ProviderHostID: side.ProviderHostID,
			ProviderForkID: side.ProviderForkID, NativeAnchorID: side.NativeAnchorID,
			Harness: side.Harness, Generation: side.Generation,
			ReferenceContext: side.ReferenceContext, ReferencePending: side.ReferencePending, SeedHistory: side.SeedHistory,
			Turns:      turns,
			Messages:   append([]domain.SideMessage(nil), m.messages[id]...),
			Activities: append([]domain.SideActivity(nil), m.activities[id]...), Draft: m.drafts[id]})
	}
	for _, side := range m.hostCleanup {
		if _, open := m.sides[side.ID]; open {
			continue
		}
		if side.AppRunID == runID {
			out = append(out, SideRecoveryRecord{CleanupOnly: true, Harness: side.Harness, HostStopped: m.hostStopped[side.ID], Side: side, ProviderHostID: side.ProviderHostID, ProviderForkID: side.ProviderForkID})
		}
	}
	return out
}

func (m *memorySideStore) recover(runID string, records []SideRecoveryRecord, now time.Time) ([]domain.SideConversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if runID == "" || runID != m.runID {
		return nil, ErrSideLaunchUnclaimed
	}
	seenIDs := map[string]bool{}
	alreadyRestored := map[string]bool{}
	created := []domain.SideConversation{}
	for _, record := range records {
		side := record.Side
		if side.ID == "" || side.MainConversationID == "" || record.ProviderHostID != "btw-"+runID+"-"+side.ID || (record.ProviderForkID == "" && side.State == "ready") || seenIDs[side.ID] {
			return nil, ErrSideUnavailable
		}
		seenIDs[side.ID] = true
		if m.closed[side.ID] {
			continue
		}
		for _, existing := range m.sides {
			if existing.ID == side.ID && existing.ClosedAt == nil {
				if existing.ID != side.ID || existing.Generation != record.Generation ||
					existing.ProviderHostID != record.ProviderHostID || existing.ProviderForkID != record.ProviderForkID {
					return nil, ErrSideUnavailable
				}
				alreadyRestored[side.ID] = true
			}
		}
	}
	for _, record := range records {
		side := record.Side
		if record.CleanupOnly {
			if alreadyRestored[side.ID] {
				return nil, ErrSideUnavailable
			}
			side.AppRunID = runID
			side.Harness = record.Harness
			side.ProviderHostID, side.ProviderForkID = record.ProviderHostID, record.ProviderForkID
			m.hostCleanup[side.ID] = side
			m.hostStopped[side.ID] = record.HostStopped
			m.closed[side.ID] = true
			if side.ProviderForkID != "" {
				m.cleanup[side.ProviderForkID] = domain.SideProviderCleanup{ForkID: side.ProviderForkID, SessionID: side.SessionID, Harness: side.Harness}
			}
			continue
		}
		if m.closed[side.ID] || alreadyRestored[side.ID] {
			continue
		}
		side.SourceProviderID = record.SourceProviderID
		side.LaunchConfig = record.LaunchConfig
		side.CreateKey = record.CreateKey
		side.AppRunID = runID
		side.ProviderHostID = record.ProviderHostID
		side.ProviderForkID = record.ProviderForkID
		side.NativeAnchorID = record.NativeAnchorID
		side.Harness = record.Harness
		side.Generation = record.Generation
		side.ReferenceContext = record.ReferenceContext
		side.ReferencePending = record.ReferencePending
		side.SeedHistory = record.SeedHistory
		side.PolicyVersion = record.PolicyVersion
		side.BoundaryTurnID = record.BoundaryTurnID
		side.BoundaryState = record.BoundaryState
		if side.ProviderForkID == "" {
			side.State = "failed"
			side.ErrorMessage = "Side opening was interrupted; close and reopen it."
		} else {
			side.State = "recovering"
		}
		if side.RecreateRequired || side.PolicyVersion != sidePolicyVersion || side.BoundaryState == "uncertain" || side.BoundaryState == "pending" {
			side.State = "failed"
			side.ErrorMessage = ErrSidePolicyRecreate.Error()
			side.RecreateRequired = true
		}
		side.UpdatedAt = now
		m.sides[side.ID] = side
		m.turns[side.ID] = make([]domain.SideTurn, 0, len(record.Turns))
		for _, item := range record.Turns {
			item.Turn.DispatchAttempt = item.DispatchAttempt
			item.Turn.Content = item.Content
			item.Turn.ProviderTurnID = item.ProviderTurnID
			for i := range item.Turn.References {
				if i < len(item.ReferenceContexts) {
					item.Turn.References[i].Context = item.ReferenceContexts[i]
				}
			}
			m.turns[side.ID] = append(m.turns[side.ID], item.Turn)
		}
		for i := range m.turns[side.ID] {
			if m.turns[side.ID][i].State == "running" && m.turns[side.ID][i].ProviderTurnID == "" {
				m.turns[side.ID][i].State = "failed"
				m.turns[side.ID][i].ErrorMessage = "Daemon restarted; retry this turn."
				m.turns[side.ID][i].CompletedAt = &now
			}
		}
		m.messages[side.ID] = append([]domain.SideMessage(nil), record.Messages...)
		m.activities[side.ID] = append([]domain.SideActivity(nil), record.Activities...)
		for i := range m.messages[side.ID] {
			message := &m.messages[side.ID][i]
			message.ProviderItemID = record.MessageProviderIDs[message.ID]
		}
		for i := range m.activities[side.ID] {
			activity := &m.activities[side.ID][i]
			activity.ProviderItemID = record.ActivityProviderIDs[activity.ID]
			for j := range activity.Decisions {
				if j < len(record.DecisionData[activity.ID]) {
					activity.Decisions[j].Raw = record.DecisionData[activity.ID][j]
				}
			}
		}
		m.drafts[side.ID] = record.Draft
		if side.ProviderForkID != "" {
			m.cleanup[side.ProviderForkID] = domain.SideProviderCleanup{ForkID: side.ProviderForkID, SessionID: side.SessionID, Harness: side.Harness}
		}
		if side.RecreateRequired && side.ProviderForkID != "" {
			m.hostCleanup[side.ID] = side
		}
		created = append(created, side)
	}
	return created, nil
}
