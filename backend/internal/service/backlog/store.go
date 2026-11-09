package backlog

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Store is the backlog service's persistence surface; the SQLite store
// satisfies it.
type Store interface {
	CreateBacklogItem(ctx context.Context, in domain.BacklogItem) (domain.BacklogItem, error)
	GetBacklogItem(ctx context.Context, id string) (domain.BacklogItem, bool, error)
	GetBacklogItemByRef(ctx context.Context, projectID domain.ProjectID, seq int64) (domain.BacklogItem, bool, error)
	ListBacklogItems(ctx context.Context, projectID domain.ProjectID, f store.BacklogFilter) ([]domain.BacklogItem, error)
	UpdateBacklogItem(ctx context.Context, id string, now time.Time, mutate func(*domain.BacklogItem) error) (domain.BacklogItem, error)
	SetBacklogItemState(ctx context.Context, id string, state domain.BacklogState, reason string, now time.Time) (domain.BacklogItem, error)
	DeleteBacklogItem(ctx context.Context, id string) ([]domain.BacklogItem, error)
	PurgeBacklog(ctx context.Context, in store.BacklogPurgeInput) (store.BacklogPurgeResult, error)
	MoveBacklogItem(ctx context.Context, id, targetID string, after bool, now time.Time) error
	NextBacklogItem(ctx context.Context, projectID domain.ProjectID, parentID string) (domain.BacklogItem, bool, error)
	CountBacklogChildren(ctx context.Context, id string) (int, error)
	CountBacklogInboxByProject(ctx context.Context) (map[string]int, error)
	CountBacklogChildrenClosed(ctx context.Context, parentID string) (int, error)
	BacklogChildStats(ctx context.Context, projectID domain.ProjectID) (map[string]store.BacklogChildStats, error)
	AddBacklogLink(ctx context.Context, itemID string, link domain.BacklogLink, now time.Time) (bool, error)
	RemoveBacklogLink(ctx context.Context, itemID string, kind domain.BacklogLinkKind, ref string, now time.Time) (bool, error)
	CloseMergedBacklogItem(ctx context.Context, id, reason string, now time.Time) (domain.BacklogItem, bool, error)
	CloseBacklogItemIfLive(ctx context.Context, id string, state domain.BacklogState, reason string, now time.Time) (domain.BacklogItem, bool, error)
	ListLiveBacklogItemsLinkedTo(ctx context.Context, kind domain.BacklogLinkKind, ref string) ([]domain.BacklogItem, error)
	ListLiveBacklogItemsWithRuntimeLinks(ctx context.Context) ([]domain.BacklogItem, error)
	BacklogFactsFor(ctx context.Context, itemID string) (store.BacklogFacts, error)
	BacklogBadgeFactsFor(ctx context.Context, projectID domain.ProjectID, itemID string) (map[string]store.BacklogBadgeFacts, error)
	GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	FileDeferredBacklog(ctx context.Context, in store.BacklogDeferInput) ([]store.BacklogDeferOutcome, error)
}
