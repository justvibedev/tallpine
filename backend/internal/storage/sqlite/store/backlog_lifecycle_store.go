package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// BacklogSessionFact is a linked session and whether it has ended.
type BacklogSessionFact struct {
	SessionID  string
	Terminated bool
}

// BacklogPRFact is a PR reachable from an item.
type BacklogPRFact struct {
	URL       string
	Number    int64
	State     string
	Title     string
	SessionID string
	// Direct is true for a pr link; false when the PR is reached through a session link.
	Direct bool
}

// BacklogFacts is everything the lifecycle rules read about one item.
type BacklogFacts struct {
	Sessions []BacklogSessionFact
	PRs      []BacklogPRFact
	// EarlierPRs are PRs a linked session opened before it was linked to the
	// item (a reused worker). They never count toward the item.
	EarlierPRs []BacklogPRFact
	// AckedPRs are merged PRs the item already had when it was manually
	// reopened. They never close it again, but they still belong to it.
	AckedPRs []BacklogPRFact
	// UntrackedPRs are pr links Tallpine has no pr row for; they can never count as merged.
	UntrackedPRs []string
}

// BacklogBadgeFacts are per-item counts behind the derived badges.
type BacklogBadgeFacts struct {
	ActiveSessions, OpenPRs, MergedPRs, ClosedPRs, UntrackedPRs int
}

// CloseBacklogItemIfLive moves an inbox/open item to done or dropped, doing
// nothing (changed=false) when it is already closed, so an automatic close can
// never overwrite a manual one that raced it.
func (s *Store) CloseBacklogItemIfLive(ctx context.Context, id string, state domain.BacklogState, reason string, now time.Time) (domain.BacklogItem, bool, error) {
	if !state.Closed() {
		return domain.BacklogItem{}, false, fmt.Errorf("%w: %s is not a closed state", domain.ErrBacklogInvalid, state)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	row, err := q.SelectBacklogItemByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BacklogItem{}, false, domain.ErrBacklogNotFound
	}
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	if domain.BacklogState(row.State).Closed() {
		item, err := backlogItemFromGen(row)
		return item, false, err
	}
	closed, err := s.setStateTx(ctx, tx, id, state, reason, now)
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BacklogItem{}, false, err
	}
	return closed, true, nil
}

// CloseMergedBacklogItem closes a live item whose linked PRs all merged. Unlike
// CloseBacklogItemIfLive it re-reads the item inside the write transaction and
// leaves scope local items alone, so a scope set to local between the caller's
// read and this write (a multi-PR item) still wins (changed=false).
func (s *Store) CloseMergedBacklogItem(ctx context.Context, id, reason string, now time.Time) (domain.BacklogItem, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := s.qw.WithTx(tx).SelectBacklogItemByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BacklogItem{}, false, domain.ErrBacklogNotFound
	}
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	if domain.BacklogState(row.State).Closed() || (row.Scope.Valid && domain.BacklogScope(row.Scope.String) == domain.BacklogScopeLocal) {
		item, err := backlogItemFromGen(row)
		return item, false, err
	}
	closed, err := s.setStateTx(ctx, tx, id, domain.BacklogDone, reason, now)
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BacklogItem{}, false, err
	}
	return closed, true, nil
}

// ListTerminatedSessionIDsWithOpenBacklogPRs returns ended sessions that still
// have an open or draft PR, linked to a backlog item or not (B397).
func (s *Store) ListTerminatedSessionIDsWithOpenBacklogPRs(ctx context.Context) ([]domain.SessionID, error) {
	return s.qr.SelectTerminatedSessionIDsWithOpenBacklogPRs(ctx)
}

// ListLiveBacklogItemsLinkedTo returns inbox/open items with a link of kind to ref.
func (s *Store) ListLiveBacklogItemsLinkedTo(ctx context.Context, kind domain.BacklogLinkKind, ref string) ([]domain.BacklogItem, error) {
	rows, err := s.qr.SelectLiveBacklogItemsLinkedTo(ctx, gen.SelectLiveBacklogItemsLinkedToParams{Kind: string(kind), Ref: ref})
	if err != nil {
		return nil, err
	}
	return backlogItemsFromGen(rows)
}

// ListLiveBacklogItemsWithRuntimeLinks returns inbox/open items linked to any
// session or PR: the sweep's work list.
func (s *Store) ListLiveBacklogItemsWithRuntimeLinks(ctx context.Context) ([]domain.BacklogItem, error) {
	rows, err := s.qr.SelectLiveBacklogItemsWithRuntimeLinks(ctx)
	if err != nil {
		return nil, err
	}
	return backlogItemsFromGen(rows)
}

// BacklogFactsFor reads the sessions and PRs behind one item.
func (s *Store) BacklogFactsFor(ctx context.Context, itemID string) (BacklogFacts, error) {
	var f BacklogFacts
	sessions, err := s.qr.SelectBacklogLinkedSessions(ctx, itemID)
	if err != nil {
		return f, err
	}
	linkedAt := map[string]time.Time{}
	for _, r := range sessions {
		f.Sessions = append(f.Sessions, BacklogSessionFact{SessionID: r.SessionID, Terminated: r.Terminated != 0})
		linkedAt[r.SessionID] = r.LinkedAt
	}
	prs, err := s.qr.SelectBacklogPRFacts(ctx, itemID)
	if err != nil {
		return f, err
	}
	acked, err := s.qr.SelectBacklogReopenAcks(ctx, itemID)
	if err != nil {
		return f, err
	}
	ackedSet := map[string]bool{}
	for _, u := range acked {
		ackedSet[u] = true
	}
	for _, r := range prs {
		fact := BacklogPRFact{URL: r.URL, Number: r.Number, State: string(r.PRState), Title: r.Title, SessionID: string(r.SessionID), Direct: r.Direct != 0}
		// A merge that predates a manual reopen no longer counts: the reopen wins.
		if fact.State == "merged" && ackedSet[fact.URL] {
			f.AckedPRs = append(f.AckedPRs, fact)
			continue
		}
		// A PR reached only through a session link counts when the session
		// opened it after the link; a reused worker's earlier PRs belong to
		// earlier work. An unknown creation time is given the benefit of the doubt.
		if !fact.Direct && r.CreatedAtProvider.Valid && !r.CreatedAtProvider.Time.IsZero() && r.CreatedAtProvider.Time.Before(linkedAt[fact.SessionID]) {
			f.EarlierPRs = append(f.EarlierPRs, fact)
			continue
		}
		f.PRs = append(f.PRs, fact)
	}
	f.UntrackedPRs, err = s.qr.SelectBacklogUntrackedPRLinks(ctx, itemID)
	return f, err
}

// BacklogBadgeFactsFor returns badge counts per item id for a project; itemID
// restricts it to one item.
func (s *Store) BacklogBadgeFactsFor(ctx context.Context, projectID domain.ProjectID, itemID string) (map[string]BacklogBadgeFacts, error) {
	rows, err := s.qr.SelectBacklogBadgeFacts(ctx, gen.SelectBacklogBadgeFactsParams{ProjectID: string(projectID), ItemID: itemID})
	if err != nil {
		return nil, err
	}
	out := make(map[string]BacklogBadgeFacts, len(rows))
	for _, r := range rows {
		out[r.ItemID] = BacklogBadgeFacts{
			ActiveSessions: int(r.ActiveSessions), OpenPRs: int(r.OpenPrs), MergedPRs: int(r.MergedPrs),
			ClosedPRs: int(r.ClosedPrs), UntrackedPRs: int(r.UntrackedPrs),
		}
	}
	return out, nil
}

// ListExpiredBacklogInbox returns agent-filed inbox items not touched since cutoff.
func (s *Store) ListExpiredBacklogInbox(ctx context.Context, cutoff time.Time) ([]domain.BacklogItem, error) {
	rows, err := s.qr.SelectExpiredBacklogInbox(ctx, cutoff)
	if err != nil {
		return nil, err
	}
	return backlogItemsFromGen(rows)
}

// ExpireBacklogInboxItem drops an untriaged inbox item, but only if, inside the
// same write transaction, it is still in the inbox and still untouched since
// cutoff. An item accepted or edited between the expiry listing and this call
// is left alone (changed=false).
func (s *Store) ExpireBacklogInboxItem(ctx context.Context, id string, cutoff time.Time, reason string, now time.Time) (domain.BacklogItem, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := s.qw.WithTx(tx).SelectBacklogItemByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BacklogItem{}, false, domain.ErrBacklogNotFound
	}
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	item, err := backlogItemFromGen(row)
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	if item.State != domain.BacklogInbox || item.Origin.Kind == domain.BacklogOriginHuman || !item.UpdatedAt.Before(cutoff) {
		return item, false, nil
	}
	closed, err := s.setStateTx(ctx, tx, id, domain.BacklogDropped, reason, now)
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BacklogItem{}, false, err
	}
	return closed, true, nil
}

// ListEndedSessionsWithOpenBacklogPRs is the SCM observer's list of ended
// sessions that still have an open or draft PR, whether or not a backlog item waits on it.
func (s *Store) ListEndedSessionsWithOpenBacklogPRs(ctx context.Context) ([]domain.SessionRecord, error) {
	ids, err := s.ListTerminatedSessionIDsWithOpenBacklogPRs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SessionRecord, 0, len(ids))
	for _, id := range ids {
		rec, ok, err := s.GetSession(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, rec)
		}
	}
	return out, nil
}
