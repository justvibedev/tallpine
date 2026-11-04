package usage

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type usageSourceIdentityKey struct {
	kind            domain.UsageSourceKind
	nativeSessionID string
	fileIdentity    string
}

type bindingSourceInventory struct {
	sources             []*domain.UsageSourceRecord
	byID                map[int64]*domain.UsageSourceRecord
	latestByPath        map[string]*domain.UsageSourceRecord
	latestCodexByNative map[string]*domain.UsageSourceRecord
	byIdentity          map[usageSourceIdentityKey][]*domain.UsageSourceRecord
	codexLogicalIDs     map[string]struct{}
	maxGeneration       int64
}

func newBindingSourceInventory(sources []domain.UsageSourceRecord) *bindingSourceInventory {
	inventory := &bindingSourceInventory{}
	for _, source := range sources {
		inventory.sources = append(inventory.sources, &domain.UsageSourceRecord{})
		*inventory.sources[len(inventory.sources)-1] = source
	}
	inventory.rebuildIndexes()
	return inventory
}

func (i *bindingSourceInventory) rebuildIndexes() {
	i.byID = make(map[int64]*domain.UsageSourceRecord, len(i.sources))
	i.latestByPath = make(map[string]*domain.UsageSourceRecord, len(i.sources))
	i.latestCodexByNative = make(map[string]*domain.UsageSourceRecord, len(i.sources))
	i.byIdentity = make(map[usageSourceIdentityKey][]*domain.UsageSourceRecord, len(i.sources))
	i.codexLogicalIDs = make(map[string]struct{}, len(i.sources))
	i.maxGeneration = 0
	for _, source := range i.sources {
		i.indexSource(source)
	}
}

func (i *bindingSourceInventory) indexSource(source *domain.UsageSourceRecord) {
	if source.ID != 0 {
		i.byID[source.ID] = source
	}
	if source.Generation >= i.maxGeneration {
		i.maxGeneration = source.Generation
	}
	latest := i.latestByPath[source.ArtifactPath]
	if latest == nil || source.Generation > latest.Generation {
		i.latestByPath[source.ArtifactPath] = source
	}
	if source.Kind == domain.UsageSourceCodexRollout && source.NativeSessionID != "" {
		i.codexLogicalIDs[source.NativeSessionID] = struct{}{}
		latest = i.latestCodexByNative[source.NativeSessionID]
		if latest == nil || source.Generation > latest.Generation ||
			(source.Generation == latest.Generation && source.ID > latest.ID) {
			i.latestCodexByNative[source.NativeSessionID] = source
		}
	}
	key := usageSourceIdentityKey{
		kind:            source.Kind,
		nativeSessionID: source.NativeSessionID,
		fileIdentity:    source.FileIdentity,
	}
	i.byIdentity[key] = append(i.byIdentity[key], source)
}

func (i *bindingSourceInventory) add(source domain.UsageSourceRecord) {
	if existing := i.byID[source.ID]; source.ID != 0 && existing != nil {
		*existing = source
		i.rebuildIndexes()
		return
	}
	record := &domain.UsageSourceRecord{}
	*record = source
	i.sources = append(i.sources, record)
	i.indexSource(record)
}

func (i *bindingSourceInventory) identityMatch(
	kind domain.UsageSourceKind,
	nativeSessionID string,
	fileIdentity string,
	size int64,
) *domain.UsageSourceRecord {
	key := usageSourceIdentityKey{
		kind:            kind,
		nativeSessionID: nativeSessionID,
		fileIdentity:    fileIdentity,
	}
	var match *domain.UsageSourceRecord
	for _, source := range i.byIdentity[key] {
		if size >= source.ByteOffset && (match == nil || source.Generation > match.Generation) {
			match = source
		}
	}
	return match
}

func (i *bindingSourceInventory) markState(
	id int64,
	state domain.UsageSourceState,
	errorCode string,
	now time.Time,
) {
	if source := i.byID[id]; source != nil {
		source.State = state
		source.LastErrorCode = errorCode
		source.NextRetryAt = nil
		source.UpdatedAt = now
	}
}

func (i *bindingSourceInventory) reactivate(id int64, now time.Time) {
	if source := i.byID[id]; source != nil {
		source.State = domain.UsageSourceActive
		source.FailureCount = 0
		source.NextRetryAt = nil
		source.LastErrorCode = ""
		source.UpdatedAt = now
	}
}

func (i *bindingSourceInventory) snapshot() []domain.UsageSourceRecord {
	sources := make([]domain.UsageSourceRecord, 0, len(i.sources))
	for _, source := range i.sources {
		sources = append(sources, *source)
	}
	return sources
}
