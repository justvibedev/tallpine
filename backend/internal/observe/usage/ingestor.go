package usage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/pricing"
	usagesvc "tallpine.eu/tallpine/backend/internal/service/usage"
)

const (
	defaultChunkBytes       = 8 << 20
	defaultRecordBytes      = 1 << 20
	defaultFinalizationWait = 2 * time.Second
)

type ingestorStore interface {
	GetUsageSourceForIngestion(context.Context, int64) (domain.UsageSourceContext, bool, error)
	ApplyUsageChunk(context.Context, int64, int64, time.Time, domain.SourceCursorState, []domain.ModelUsageEvent) error
	HasOpenUsageAttribution(context.Context, int64) (bool, error)
	MarkUsageSourceState(context.Context, int64, domain.UsageSourceState, string, *time.Time, time.Time) (bool, error)
	MarkUsageSourceFailure(context.Context, int64, int64, string, time.Time, time.Time) (bool, error)
	ReplaceUsageSource(context.Context, int64, string, domain.UsageSourceRecord, time.Time) (domain.UsageSourceRecord, error)
	CompleteUsageBindingIfSettled(context.Context, int64, time.Time) (bool, error)
	UpdateUsageBindingState(context.Context, int64, domain.UsageBindingState, string, time.Time) (bool, error)
}

// IngestorConfig controls bounded processing of one transcript source.
type IngestorConfig struct {
	ChunkBytes       int64
	RecordBytes      int
	FinalizationWait time.Duration
	Clock            func() time.Time
	Pricing          *pricing.Manager
	OnPricingError   func(error)
	// RequestAttributionRepair asks for a legacy repair pass. See
	// notifyLateRouteEvidence for the one window it closes.
	RequestAttributionRepair func()
}

// IngestResult tells the coordinator whether another immediate chunk, source
// inventory refresh, or delayed retry is required.
type IngestResult struct {
	More                bool
	Refresh             bool
	SyncWatch           bool
	Reconcile           bool
	ReconcilePath       string
	RetryAt             *time.Time
	ReplacementSourceID int64
}

// Ingestor incrementally processes registered transcript files when the
// watcher, a hook, startup reconciliation, or a retry timer requests work.
type Ingestor struct {
	store            ingestorStore
	chunkBytes       int64
	recordBytes      int
	finalizationWait time.Duration
	now              func() time.Time
	pricing          *pricing.Manager
	onPricingError   func(error)
	requestRepair    func()
	openTranscript   func(string) (*os.File, error)
	afterRead        func()
}

// NewIngestor constructs a bounded transcript ingestor.
func NewIngestor(store ingestorStore, cfg IngestorConfig) *Ingestor {
	if cfg.ChunkBytes <= 0 {
		cfg.ChunkBytes = defaultChunkBytes
	}
	if cfg.RecordBytes <= 0 {
		cfg.RecordBytes = defaultRecordBytes
	}
	if cfg.FinalizationWait <= 0 {
		cfg.FinalizationWait = defaultFinalizationWait
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.OnPricingError == nil {
		cfg.OnPricingError = func(error) {}
	}
	return &Ingestor{
		store:            store,
		chunkBytes:       cfg.ChunkBytes,
		recordBytes:      cfg.RecordBytes,
		finalizationWait: cfg.FinalizationWait,
		now:              cfg.Clock,
		pricing:          cfg.Pricing,
		onPricingError:   cfg.OnPricingError,
		requestRepair:    cfg.RequestAttributionRepair,
		openTranscript:   os.Open,
	}
}

// Ingest processes one source from its durable cursor. It never loops on an
// incomplete trailing record; a later filesystem event will enqueue the source
// again after the provider finishes the record.
func (i *Ingestor) Ingest(ctx context.Context, sourceID int64) (IngestResult, error) {
	if err := ctx.Err(); err != nil {
		return IngestResult{}, err
	}
	now := i.now().UTC()
	source, ok, err := i.store.GetUsageSourceForIngestion(ctx, sourceID)
	if err != nil || !ok {
		return IngestResult{}, err
	}
	result := IngestResult{
		SyncWatch: source.Source.State == domain.UsageSourceError &&
			source.Source.LastErrorCode == domain.UsageErrorArtifactMissing,
	}
	parserState, err := decodeParserState(source.Source)
	if err != nil {
		return i.retrySource(
			ctx,
			source.Source,
			domain.UsageErrorInvalidParserState,
			now,
			fmt.Errorf("decode parser state: %w", err),
		)
	}
	file, err := i.openTranscript(source.Source.ArtifactPath) //nolint:gosec // path was validated at registration.
	if err != nil {
		return i.retrySource(ctx, source.Source, domain.UsageErrorArtifactMissing, now, nil)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return i.retrySource(ctx, source.Source, domain.UsageErrorArtifactMissing, now, nil)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	identity, err := usagesvc.SourceIdentityFromFile(file)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, nil)
	}
	if source.Source.FileIdentity != identity || info.Size() < source.Source.ByteOffset {
		return i.resetSourceGeneration(ctx, source, identity, now)
	}
	if source.Source.ByteOffset > 0 && parserState.Integrity.Checkpoint == nil {
		return i.resetSourceGeneration(ctx, source, identity, now)
	}
	cursorSample, err := parserCheckpointSampleAt(file, source.Source.ByteOffset)
	if err != nil {
		return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, nil)
	}
	snapshot := transcriptSnapshot{
		identity:     identity,
		size:         info.Size(),
		modTime:      info.ModTime(),
		cursorSample: cursorSample,
	}
	if !parserCheckpointsEqual(cursorSample.checkpoint, parserState.Integrity.Checkpoint) {
		return i.resetSourceGeneration(ctx, source, identity, now)
	}
	if source.Source.State == domain.UsageSourceComplete &&
		source.BindingState != domain.UsageBindingFinalizing &&
		info.Size() == source.Source.ByteOffset {
		return result, nil
	}
	if err := i.markBindingFinalizing(ctx, &source, info.Size(), now); err != nil {
		return result, err
	}
	chunk, err := readJSONLChunkFromSnapshot(
		ctx,
		file,
		snapshot.size,
		source.Source.ByteOffset,
		i.chunkBytes,
		i.recordBytes,
		parserState.Integrity.DiscardingOversizedRecord,
	)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, nil)
	}
	if i.afterRead != nil {
		i.afterRead()
	}
	finalizationSettled := source.Source.NextRetryAt != nil && !source.Source.NextRetryAt.After(now)
	tail := i.settleFinalizingTail(source, parserState, &chunk, finalizationSettled)
	if !codexChunkAttributionMatches(source.Source, parserState.Codex, chunk.records) {
		return i.resetSourceGeneration(ctx, source, identity, now)
	}
	checkpoint, err := parserCheckpointAfterRead(
		snapshot.cursorSample,
		source.Source.ByteOffset,
		chunk.nextOffset,
		chunk.data,
	)
	if err != nil {
		return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, nil)
	}
	parserState.Integrity.Checkpoint = checkpoint
	parserState.Integrity.DiscardingOversizedRecord = chunk.discardingOversizedRecord
	parsed := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, parserState)
	if parsed.err != nil {
		return i.retrySource(ctx, source.Source, domain.UsageErrorInvalidParserState, now, parsed.err)
	}
	if chunk.anomalies > 0 {
		parsed.Cursor.AnomalyCount += int64(chunk.anomalies)
		parsed.Cursor.LastErrorCode = chunk.errorCode
	}
	progressed := chunk.nextOffset > source.Source.ByteOffset
	i.applyFinalizationCursor(source, &parsed, &result, &chunk, tail, finalizationSettled, progressed, now)
	unchanged, err := transcriptSnapshotUnchanged(file, source.Source.ByteOffset, snapshot)
	if err != nil {
		return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, err)
	}
	if !unchanged {
		return i.retrySource(
			ctx,
			source.Source,
			domain.UsageErrorSourceReadFailed,
			now,
			errors.New("transcript changed during ingestion"),
		)
	}
	if err := i.applyParsedChunk(ctx, source, &parsed); err != nil {
		if errors.Is(err, domain.ErrUsageSourceEventConflict) {
			return result, i.completeConflictedSource(ctx, source, err, now)
		}
		return result, fmt.Errorf("apply usage source %d: %w", source.Source.ID, err)
	}
	i.notifyLateRouteEvidence(ctx, source.Source.ID)
	result.Reconcile = parsed.newCodexChild
	if parsed.Cursor.State == domain.UsageSourceComplete {
		return result, i.completeBinding(ctx, source.Source.BindingID, now)
	}
	result.More = progressed && !chunk.atEOF && !chunk.readToEOF
	return result, nil
}

// notifyLateRouteEvidence asks for a repair pass when a chunk landed
// unattributed events on a binding that already knows its route.
//
// The hook that resolves a route fires the repair trigger exactly once. A chunk
// parsed before that hook but applied after it slips straight through the pass
// it fired: those rows did not exist yet when the pass read the table, and
// nothing fires again for this binding until the daemon restarts. Re-reading
// the source is the point — the context this chunk was parsed against is
// precisely the stale one.
func (i *Ingestor) notifyLateRouteEvidence(ctx context.Context, sourceID int64) {
	if i.requestRepair == nil {
		return
	}
	// A binding with no route yet leaves rows open on every chunk, and its own
	// hook will fire the trigger when one arrives; a route the hook could not
	// name is never going to produce one. Only a usable route is worth a pass.
	source, ok, err := i.store.GetUsageSourceForIngestion(ctx, sourceID)
	if err != nil || !ok {
		return
	}
	switch strings.TrimSpace(source.ProviderHint) {
	case "", pricing.UnidentifiedBillingRoute:
		return
	}
	// Asking the table rather than the parsed chunk catches the second way a
	// row is left open under a known route: a replaced transcript re-emits an
	// event, the replay deduplicates against the stored row and rehomes it, and
	// the parsed event carrying the route is discarded with the duplicate.
	open, err := i.store.HasOpenUsageAttribution(ctx, sourceID)
	if err != nil || !open {
		return
	}
	i.requestRepair()
}
