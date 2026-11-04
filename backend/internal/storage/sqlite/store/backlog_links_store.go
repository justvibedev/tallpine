package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

func backlogLinksFromGen(rows []gen.BacklogLink) []domain.BacklogLink {
	out := make([]domain.BacklogLink, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.BacklogLink{Kind: domain.BacklogLinkKind(r.Kind), Ref: r.Ref, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt})
	}
	return out
}

// AddBacklogLink links an item to a session, PR, issue, artifact, memory or
// another item. Item links must stay in the project and a blocked_by edge must
// not close a cycle. It reports whether a new link was created (re-adding is a
// no-op) and bumps the item's updated_at.
func (s *Store) AddBacklogLink(ctx context.Context, itemID string, link domain.BacklogLink, now time.Time) (bool, error) {
	link.Ref = strings.TrimSpace(link.Ref)
	if !link.Kind.Valid() || link.Ref == "" {
		return false, fmt.Errorf("%w: link kind or ref is invalid", domain.ErrBacklogInvalid)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	item, err := q.SelectBacklogItemByID(ctx, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, domain.ErrBacklogNotFound
	}
	if err != nil {
		return false, err
	}
	if link.Kind.IsItemLink() {
		if link.Ref == itemID {
			return false, fmt.Errorf("%w: an item cannot link to itself", domain.ErrBacklogInvalid)
		}
		other, err := q.SelectBacklogItemByID(ctx, link.Ref)
		if errors.Is(err, sql.ErrNoRows) {
			return false, domain.ErrBacklogNotFound
		}
		if err != nil {
			return false, err
		}
		if other.ProjectID != item.ProjectID {
			return false, domain.ErrBacklogCrossProject
		}
		if link.Kind == domain.BacklogLinkBlockedBy {
			cyclic, err := backlogBlockedByReaches(ctx, q, link.Ref, itemID)
			if err != nil {
				return false, err
			}
			if cyclic {
				return false, domain.ErrBacklogCycle
			}
		}
	}
	n, err := q.InsertBacklogLink(ctx, gen.InsertBacklogLinkParams{
		ItemID: itemID, Kind: string(link.Kind), Ref: link.Ref, CreatedBy: link.CreatedBy, CreatedAt: now,
	})
	if err != nil {
		return false, fmt.Errorf("insert backlog link: %w", err)
	}
	if n > 0 {
		if err := q.TouchBacklogItem(ctx, gen.TouchBacklogItemParams{UpdatedAt: now, ID: itemID}); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// RemoveBacklogLink deletes one link, reporting whether it existed.
func (s *Store) RemoveBacklogLink(ctx context.Context, itemID string, kind domain.BacklogLinkKind, ref string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	n, err := q.DeleteBacklogLink(ctx, gen.DeleteBacklogLinkParams{ItemID: itemID, Kind: string(kind), Ref: strings.TrimSpace(ref)})
	if err != nil {
		return false, err
	}
	if n > 0 {
		if err := q.TouchBacklogItem(ctx, gen.TouchBacklogItemParams{UpdatedAt: now, ID: itemID}); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// backlogBlockedByReaches reports whether to is reachable from from by
// following blocked_by edges (from is blocked by X, X by Y, ...).
func backlogBlockedByReaches(ctx context.Context, q *gen.Queries, from, to string) (bool, error) {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			return true, nil
		}
		refs, err := q.SelectBacklogBlockers(ctx, cur)
		if err != nil {
			return false, err
		}
		for _, r := range refs {
			if !seen[r] {
				seen[r] = true
				queue = append(queue, r)
			}
		}
	}
	return false, nil
}
