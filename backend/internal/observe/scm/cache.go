package scm

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/observe"
)

// ObserverCache stores provider ETags and review polling timestamps in memory.
// It is intentionally non-persistent for v1; cold restarts simply revalidate.
type ObserverCache struct {
	// RepoPRListETag maps repository keys to the last open-PR-list ETag.
	RepoPRListETag map[string]string
	// CommitChecksETag maps repo+commit keys to the last check-runs ETag.
	CommitChecksETag map[string]string
	// LastReviewPollAt maps PR keys to the last review-thread fetch timestamp.
	LastReviewPollAt map[string]time.Time
	// ReviewRefreshFailed marks PRs whose review-thread refresh failed; the
	// next poll retries regardless of the normal review cadence/status rules.
	ReviewRefreshFailed map[string]bool
	// LastPRFetchAt maps PR keys to the last time the PR was successfully fetched from the provider.
	LastPRFetchAt map[string]time.Time
	// lastPRFetchOrder tracks FIFO eviction order for LastPRFetchAt.
	lastPRFetchOrder []string
	// LastSyncCursor maps repository keys to the last successful incremental
	// PR-list sync timestamp. ListPRsByRepo is called with updated_after =
	// cursor - overlap; the cursor advances only after successful persistence
	//
	LastSyncCursor map[string]time.Time
	// repoOrder tracks FIFO eviction order for RepoPRListETag.
	repoOrder []string
	// commitOrder tracks FIFO eviction order for CommitChecksETag.
	commitOrder []string
	// lastReviewPollOrder tracks FIFO eviction order for LastReviewPollAt.
	lastReviewPollOrder []string
	// reviewFailedOrder tracks FIFO eviction order for ReviewRefreshFailed.
	reviewFailedOrder []string
	// max is the maximum number of entries each cache map retains.
	max int
}

func newCache(maxEntries int) ObserverCache {
	if maxEntries <= 0 {
		maxEntries = DefaultCacheMax
	}
	return ObserverCache{
		RepoPRListETag:      map[string]string{},
		CommitChecksETag:    map[string]string{},
		LastReviewPollAt:    map[string]time.Time{},
		ReviewRefreshFailed: map[string]bool{},
		LastPRFetchAt:       map[string]time.Time{},
		LastSyncCursor:      map[string]time.Time{},
		max:                 maxEntries,
	}
}

// cacheSetString / cacheSetTime / cacheSetBool are thin wrappers around the
// generic observe.CacheSet helper, kept on Observer so callers don't need to
// thread o.Cache.max through every invocation. The single shared
// implementation lives in the observe package.
func (o *Observer) cacheSetString(m map[string]string, order *[]string, key, value string) {
	observe.CacheSet(m, order, o.Cache.max, key, value)
}

func (o *Observer) cacheSetTime(m map[string]time.Time, order *[]string, key string, value time.Time) {
	observe.CacheSet(m, order, o.Cache.max, key, value)
}

func (o *Observer) cacheSetBool(m map[string]bool, order *[]string, key string, value bool) {
	observe.CacheSet(m, order, o.Cache.max, key, value)
}

func cacheDelete[V any](m map[string]V, order *[]string, key string) {
	observe.CacheDelete(m, order, key)
}
