package usage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	maxUsageMetadataBytes          = 256
	maxUsagePathBytes              = 4096
	maxCodexSessionMeta            = 1 << 20
	maxCodexChildIDs               = 4096
	defaultCodexLogicalSourceLimit = 4096
	defaultDiscoveryLimit          = 64
)

var nativeUsageIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ErrUsageSessionNotFound reports that hook metadata targeted no durable Tallpine session.
var ErrUsageSessionNotFound = errors.New("usage session not found")

type validatedSourceArtifact struct {
	path     string
	identity string
	size     int64
}

// HookSignal is the usage-specific metadata carried by a Tallpine agent hook.
type HookSignal struct {
	Harness                domain.AgentHarness
	ProviderHint           string
	Event                  string
	LaunchID               string
	NativeSessionID        string
	ModelID                string
	TranscriptPath         string
	SubagentID             string
	SubagentTranscriptPath string
}

// SourceRoots are the provider-owned directories from which Tallpine may read usage
// transcripts.
type SourceRoots struct {
	ClaudeProjects string
	CodexSessions  string
	CodexArchived  string
}

type collectorStore interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListAllSessions(context.Context) ([]domain.SessionRecord, error)
	UpsertUsageBinding(context.Context, domain.UsageBindingRecord) (domain.UsageBindingRecord, error)
	GetUsageBinding(context.Context, domain.SessionID, domain.AgentHarness, string) (domain.UsageBindingRecord, bool, error)
	ListUsageBindingsForSession(context.Context, domain.SessionID) ([]domain.UsageBindingRecord, error)
	FinalizeUsageBindingsForSessionLaunch(context.Context, domain.SessionID, string, int64, time.Time) ([]domain.UsageBindingRecord, error)
	ListUsageDiscoveryBindings(context.Context, int64) ([]domain.UsageBindingRecord, error)
	ListUsageBindingsForCodexParent(context.Context, string) ([]domain.UsageBindingRecord, error)
	ListLatestRetiredCodexReplacementClaimsByPath(context.Context, string) ([]domain.UsageSourceRecord, error)
	UpdateUsageBindingState(context.Context, int64, domain.UsageBindingState, string, time.Time) (bool, error)
	UpdateUsageBindingErrorCode(context.Context, int64, string, time.Time) (bool, error)
	CompleteUsageBindingIfSettled(context.Context, int64, time.Time) (bool, error)
	InsertUsageSource(context.Context, domain.UsageSourceRecord) (domain.UsageSourceRecord, error)
	ReplaceUsageSource(context.Context, int64, string, domain.UsageSourceRecord, time.Time) (domain.UsageSourceRecord, error)
	ListUsageSourcesForBinding(context.Context, int64) ([]domain.UsageSourceRecord, error)
	HasUsageSourceByPath(context.Context, string) (bool, error)
	ReactivateUsageSource(context.Context, int64, time.Time) (bool, error)
}

// Collector registers provider transcript files and coordinates their source
// lifecycle. Parsing and cursor advancement remain in the usage ingestor.
type Collector struct {
	store                   collectorStore
	roots                   SourceRoots
	notifySourcesChanged    func(reconcile bool)
	notifyRouteResolved     func()
	codexLogicalSourceLimit int
	now                     func() time.Time
	logger                  *slog.Logger
	mu                      sync.Mutex
	// Guarded separately from mu, which RecordHook holds across the whole hook.
	routeMu sync.RWMutex
}

// OnRouteResolved registers the handler called the first time a binding learns
// its billing route. Only Claude needs it: a Claude transcript names no
// provider, so events collected before the first hook are unattributed, and
// this is the moment the evidence to repair them finally exists.
func (c *Collector) OnRouteResolved(handler func()) {
	if c == nil {
		return
	}
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	c.notifyRouteResolved = handler
}

func (c *Collector) routeResolvedHandler() func() {
	c.routeMu.RLock()
	defer c.routeMu.RUnlock()
	return c.notifyRouteResolved
}

// NewCollector constructs a transcript source registrar.
func NewCollector(store collectorStore, roots SourceRoots, notifySourcesChanged func(reconcile bool)) *Collector {
	return newCollectorWithCodexSourceLimit(store, roots, notifySourcesChanged, defaultCodexLogicalSourceLimit)
}

func newCollectorWithCodexSourceLimit(
	store collectorStore,
	roots SourceRoots,
	notifySourcesChanged func(reconcile bool),
	limit int,
) *Collector {
	if limit <= 0 {
		limit = defaultCodexLogicalSourceLimit
	}
	return &Collector{
		store:                   store,
		roots:                   roots,
		notifySourcesChanged:    notifySourcesChanged,
		codexLogicalSourceLimit: limit,
		now:                     time.Now,
		logger:                  slog.Default(),
	}
}

// FinalizeSession moves every known native binding for one observed runtime
// generation and session revision into finalization, then asks the ingestion
// pipeline to collect a stable final cursor. It is idempotent and safe to call
// before the session is terminated.
func (c *Collector) FinalizeSession(
	ctx context.Context,
	sessionID domain.SessionID,
	expectedRuntimeLaunchID string,
	expectedSessionRevision int64,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now().UTC()
	bindings, err := c.store.FinalizeUsageBindingsForSessionLaunch(
		ctx,
		sessionID,
		boundedUsageMetadata(expectedRuntimeLaunchID),
		expectedSessionRevision,
		now,
	)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if _, err := c.reactivateLatestSources(ctx, binding.ID, now); err != nil {
			return err
		}
	}
	if len(bindings) > 0 {
		c.notifySourceInventory(false)
	}
	return nil
}

// ReactivateSession resumes collection for the native session relaunched by Tallpine.
// Existing cursors and events remain untouched; the source inventory is merely
// made watchable again so hooks are not required for continued accounting.
func (c *Collector) ReactivateSession(
	ctx context.Context,
	sessionID domain.SessionID,
	expectedRuntimeLaunchID string,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	session, ok, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrUsageSessionNotFound, sessionID)
	}
	if session.IsTerminated || session.Metadata.RuntimeLaunchID != expectedRuntimeLaunchID ||
		!SupportedHarness(session.Harness) {
		return nil
	}

	bindings, err := c.store.ListUsageBindingsForSession(ctx, sessionID)
	if err != nil {
		return err
	}
	nativeID := usageNativeSessionID(session)
	var current *domain.UsageBindingRecord
	for index := len(bindings) - 1; index >= 0; index-- {
		binding := &bindings[index]
		if binding.Harness != session.Harness || nativeID != "" && binding.NativeRootID != nativeID {
			continue
		}
		current = binding
		break
	}
	if current != nil {
		if _, err := c.reactivateBinding(ctx, *current, c.now().UTC()); err != nil {
			return err
		}
	} else if nativeID != "" && nativeUsageIDPattern.MatchString(nativeID) {
		if err := c.backfillSession(ctx, session, nativeID); err != nil {
			return err
		}
	} else {
		return nil
	}
	c.notifySourceInventory(true)
	return nil
}

// maxClaudeContinuationScanTotalBytes bounds the total bytes read per
// candidate, so a pathological run-on record cannot make the scan unbounded.
const maxClaudeContinuationScanTotalBytes = 8 << 20

// maxClaudeContinuationScanRecords bounds the leading-record window.
const maxClaudeContinuationScanRecords = 128

func latestCodexSourceDiscovers(sources []domain.UsageSourceRecord, parentID, childID string) bool {
	for _, source := range latestCodexSourcesByNativeSession(sources) {
		if source.NativeSessionID == parentID && slices.Contains(discoveredCodexChildIDs(source.ParserStateJSON), childID) {
			return true
		}
	}
	return false
}

func (c *Collector) settleFinalizingBinding(ctx context.Context, bindingID int64, now time.Time) error {
	_, err := c.store.CompleteUsageBindingIfSettled(ctx, bindingID, now)
	return err
}

func (c *Collector) reactivateBinding(ctx context.Context, binding domain.UsageBindingRecord, now time.Time) (bool, error) {
	state := domain.UsageBindingActive
	errorCode := ""
	if binding.LastErrorCode == domain.UsageErrorCodexSourceBudgetExceeded {
		errorCode = binding.LastErrorCode
		state = domain.UsageBindingPartial
	}
	if _, err := c.store.UpdateUsageBindingState(ctx, binding.ID, state, errorCode, now); err != nil {
		return false, err
	}
	return c.reactivateLatestSources(ctx, binding.ID, now)
}

func (c *Collector) reactivateLatestSources(ctx context.Context, bindingID int64, now time.Time) (bool, error) {
	sources, err := c.store.ListUsageSourcesForBinding(ctx, bindingID)
	if err != nil {
		return false, err
	}
	latestByPath := make(map[string]domain.UsageSourceRecord)
	for _, source := range sources {
		key := source.ArtifactPath
		if source.Kind == domain.UsageSourceCodexRollout && source.NativeSessionID != "" {
			key = string(source.Kind) + "\x00" + source.NativeSessionID
		}
		latest, ok := latestByPath[key]
		if !ok || source.Generation > latest.Generation ||
			(source.Generation == latest.Generation && source.ID > latest.ID) {
			latestByPath[key] = source
		}
	}
	changed := false
	for _, source := range latestByPath {
		sourceChanged, err := c.store.ReactivateUsageSource(ctx, source.ID, now)
		if err != nil {
			return false, err
		}
		changed = changed || sourceChanged
	}
	return changed, nil
}

func (c *Collector) finalizeSession(ctx context.Context, sessionID domain.SessionID, now time.Time) error {
	bindings, err := c.store.ListUsageBindingsForSession(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		errorCode := ""
		if binding.LastErrorCode == domain.UsageErrorCodexSourceBudgetExceeded {
			errorCode = binding.LastErrorCode
		}
		if _, err := c.store.UpdateUsageBindingState(ctx, binding.ID, domain.UsageBindingFinalizing, errorCode, now); err != nil {
			return err
		}
		if _, err := c.reactivateLatestSources(ctx, binding.ID, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collector) notifySourceInventory(reconcile bool) {
	if c.notifySourcesChanged != nil {
		c.notifySourcesChanged(reconcile)
	}
}

func boundedUsageMetadata(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxUsageMetadataBytes {
		return ""
	}
	return value
}

// SourceIdentity returns the filesystem's stable file id. Transcript contents
// are append-only and therefore cannot participate in identity without making a
// newly created or partially written first record look like file replacement.
func SourceIdentity(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := os.Open(path) //nolint:gosec // validated provider-owned path.
	if err != nil {
		return "", errors.New(domain.UsageErrorSourceReadFailed)
	}
	defer func() { _ = file.Close() }()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return SourceIdentityFromFile(file)
}

// SourceIdentityFromFile returns the filesystem's stable id for an already
// opened transcript descriptor.
func SourceIdentityFromFile(file *os.File) (string, error) {
	fileID, err := sourceFileID(file)
	if err != nil {
		return "", errors.New(domain.UsageErrorSourceReadFailed)
	}
	return fileID, nil
}
