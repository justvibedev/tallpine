package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

// Conversation history retention below the last settled compaction boundary.
//
// Compaction shrinks the model's context, not the database: a long-lived
// orchestrator conversation kept every provider event and every command's full
// output (585k events and 21k activities for one conversation). Once a
// compaction has settled, what came before it is history the model no longer
// sees, so the janitor bounds it:
//
//   - Provider events below the boundary lose their payload. A row whose
//     identity no replay can send again is deleted: no identity at all, or a
//     persistent-host journal identity (acp-host:...), which the host discards
//     once Tallpine acknowledges the prompt. Any other identity (native-history
//     imports, codex notifications) is kept as a '{}' tombstone so the dedupe
//     index still rejects a replay. turn.completed and thread.compacted rows are
//     kept whole: authentication verification reads the former, the boundary
//     search the latter.
//   - Settled command, reasoning, file-change and MCP-tool activities created
//     before the boundary keep their kind, status, summary and a short head of
//     each long field the timeline can mark as cut, with that field's flag
//     set (see prunedActivityDetail). Other kinds, user steers included, and
//     messages and turns are never touched, so the visible transcript stays
//     complete.
//
// Every method does one bounded unit of work under the writer lock, like the
// provider-event compaction it runs beside.

const (
	// HistoryPruneKeepBytes is how much of a long text a pruned activity keeps.
	HistoryPruneKeepBytes = 2048
	// historyPruneMinBytes skips activities too small to be worth a write.
	historyPruneMinBytes = 2 * HistoryPruneKeepBytes
	// persistentHostEventPrefix marks provider event identities minted from a
	// persistent host's prompt journal (persistenthost.acpRelay.eventID).
	persistentHostEventPrefix = "acp-host:"
)

// HistoryRetentionCandidate is a conversation with history to prune below a
// compaction boundary.
type HistoryRetentionCandidate struct {
	ConversationID string
	// LatestCompactionID is the newest thread.compacted event, settled or not.
	LatestCompactionID int64
	// BoundaryEventID is the settled boundary the cursors work toward.
	BoundaryEventID            int64
	EventsPrunedThroughID      int64
	ActivitiesPrunedThroughSeq int64
	ActivitiesDoneBoundaryID   int64
}

// HistoryEventPrune reports one provider-event window.
type HistoryEventPrune struct {
	WriterHold
	Deleted    int64
	Tombstoned int64
	// Next is the cursor after the window: the boundary once Done.
	Next int64
	Done bool
}

// HistoryActivityPrune reports one activity window.
type HistoryActivityPrune struct {
	WriterHold
	Pruned     int64
	BytesFreed int64
	// Watermark is the persisted cursor: every activity at or below it is done.
	// An activity still running in a live turn holds it below itself.
	Watermark int64
	// Next is where the following window starts, past any held activity.
	Next int64
	// Done reports that the window reached the boundary.
	Done bool
}

// HistoryRetentionCandidates lists conversations that compacted and still have
// history to prune, or a newer compaction to check.
func (s *Store) HistoryRetentionCandidates(ctx context.Context) ([]HistoryRetentionCandidate, error) {
	rows, err := s.qr.ListHistoryRetentionCandidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list history retention candidates: %w", err)
	}
	out := make([]HistoryRetentionCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, HistoryRetentionCandidate{
			ConversationID:             row.ConversationID,
			LatestCompactionID:         row.LatestCompactionID,
			BoundaryEventID:            row.BoundaryEventID,
			EventsPrunedThroughID:      row.EventsPrunedThroughID,
			ActivitiesPrunedThroughSeq: row.ActivitiesPrunedThroughSeq,
			ActivitiesDoneBoundaryID:   row.ActivitiesDoneBoundaryID,
		})
	}
	return out, nil
}

// SettledHistoryBoundary returns the newest settled compaction above after and
// the time it was received, or after and a zero time when there is none.
func (s *Store) SettledHistoryBoundary(ctx context.Context, conversationID string, after int64) (int64, time.Time, error) {
	id, err := s.qr.SettledHistoryCompactionBoundary(ctx, gen.SettledHistoryCompactionBoundaryParams{
		ConversationID: conversationID,
		AfterID:        after,
	})
	if err != nil {
		return after, time.Time{}, fmt.Errorf("settled compaction boundary of %s: %w", conversationID, err)
	}
	if id == 0 {
		id = after
	}
	if id == 0 {
		return 0, time.Time{}, nil
	}
	at, err := s.qr.HistoryBoundaryReceivedAt(ctx, id)
	if err != nil {
		return after, time.Time{}, fmt.Errorf("compaction boundary %d time: %w", id, err)
	}
	return id, at, nil
}

// PruneHistoryEvents prunes one window of up to limit provider events above
// after and below boundary.
func (s *Store) PruneHistoryEvents(
	ctx context.Context,
	conversationID string,
	boundary, after, limit int64,
	now time.Time,
) (HistoryEventPrune, error) {
	release, err := s.holdWriter(ctx)
	if err != nil {
		return HistoryEventPrune{Next: after}, err
	}
	result, err := s.pruneHistoryEventsLocked(ctx, conversationID, boundary, after, limit, now)
	result.WriterHold = release()
	return result, err
}

func (s *Store) pruneHistoryEventsLocked(
	ctx context.Context,
	conversationID string,
	boundary, after, limit int64,
	now time.Time,
) (HistoryEventPrune, error) {
	result := HistoryEventPrune{Next: after}
	err := s.inTx(ctx, "prune history events", func(q *gen.Queries) error {
		rows, err := q.SelectProviderEventsForHistoryPrune(ctx, gen.SelectProviderEventsForHistoryPruneParams{
			ConversationID: conversationID,
			AfterID:        after,
			BoundaryID:     boundary,
			PageLimit:      limit,
		})
		if err != nil {
			return err
		}
		var deletes, tombstones []int64
		for _, row := range rows {
			switch {
			case row.Method == "turn.completed" || row.Method == "thread.compacted":
			case row.ProviderEventID == "" || strings.HasPrefix(row.ProviderEventID, persistentHostEventPrefix):
				deletes = append(deletes, row.ID)
			default:
				tombstones = append(tombstones, row.ID)
			}
		}
		if len(deletes) > 0 {
			if result.Deleted, err = q.DeleteProviderEventsByID(ctx, deletes); err != nil {
				return err
			}
		}
		if len(tombstones) > 0 {
			if result.Tombstoned, err = q.TombstoneProviderEventsByID(ctx, tombstones); err != nil {
				return err
			}
		}
		result.Done = int64(len(rows)) < limit
		if result.Done {
			result.Next = boundary
		} else {
			result.Next = rows[len(rows)-1].ID
		}
		return q.UpsertHistoryRetention(ctx, gen.UpsertHistoryRetentionParams{
			ConversationID:        conversationID,
			BoundaryEventID:       boundary,
			EventsPrunedThroughID: result.Next,
			UpdatedAt:             now,
		})
	})
	if err != nil {
		return HistoryEventPrune{Next: after}, fmt.Errorf("prune history events of %s: %w", conversationID, err)
	}
	return result, nil
}

// PruneHistoryActivities prunes one window of up to limit activities above
// after that were created before boundaryAt. watermark is the persisted cursor;
// after equals it on a pass's first window and moves ahead of it once an
// earlier window held an activity that is still running.
func (s *Store) PruneHistoryActivities(
	ctx context.Context,
	conversationID string,
	boundary int64,
	boundaryAt time.Time,
	watermark, after, limit int64,
	now time.Time,
) (HistoryActivityPrune, error) {
	release, err := s.holdWriter(ctx)
	if err != nil {
		return HistoryActivityPrune{Watermark: watermark, Next: after}, err
	}
	result, err := s.pruneHistoryActivitiesLocked(ctx, conversationID, boundary, boundaryAt, watermark, after, limit, now)
	result.WriterHold = release()
	return result, err
}

func (s *Store) pruneHistoryActivitiesLocked(
	ctx context.Context,
	conversationID string,
	boundary int64,
	boundaryAt time.Time,
	watermark, after, limit int64,
	now time.Time,
) (HistoryActivityPrune, error) {
	result := HistoryActivityPrune{Watermark: watermark, Next: after}
	err := s.inTx(ctx, "prune history activities", func(q *gen.Queries) error {
		rows, err := q.SelectActivitiesForHistoryPrune(ctx, gen.SelectActivitiesForHistoryPruneParams{
			ConversationID: conversationID,
			AfterSequence:  after,
			PageLimit:      limit,
		})
		if err != nil {
			return err
		}
		result.Done = int64(len(rows)) < limit
		var held int64
		for _, row := range rows {
			if !row.CreatedAt.Before(boundaryAt) {
				result.Done = true
				break
			}
			result.Next = row.Sequence
			if row.Status == domain.ActivityStatusRunning || row.Status == domain.ActivityStatusPending {
				// Never shorten an activity that has not settled. Only one whose
				// turn is still live holds the cursor: real archives carry plan
				// and approval rows left running by turns that ended weeks ago,
				// and holding on those would rescan everything after them on
				// every pass.
				if turn := domain.TurnState(row.TurnState); (turn == domain.TurnStateQueued || turn == domain.TurnStateRunning) && held == 0 {
					held = row.Sequence
				}
				continue
			}
			pruned, freed, ok := prunedActivityPayload(row)
			if !ok {
				continue
			}
			if err := q.PruneActivityPayload(ctx, pruned); err != nil {
				return err
			}
			result.Pruned++
			result.BytesFreed += freed
		}
		switch {
		case watermark < after:
			// An earlier window of this pass held an activity.
		case held != 0:
			result.Watermark = max(watermark, held-1)
		default:
			result.Watermark = max(watermark, result.Next)
		}
		params := gen.UpsertHistoryRetentionParams{
			ConversationID:             conversationID,
			BoundaryEventID:            boundary,
			ActivitiesPrunedThroughSeq: result.Watermark,
			UpdatedAt:                  now,
		}
		if result.Done && held == 0 && watermark >= after {
			params.ActivitiesDoneBoundaryID = boundary
		}
		return q.UpsertHistoryRetention(ctx, params)
	})
	if err != nil {
		return HistoryActivityPrune{Watermark: watermark, Next: after}, fmt.Errorf("prune history activities of %s: %w", conversationID, err)
	}
	return result, nil
}

// prunedActivityPayload shortens a settled provider-output activity's long
// texts to a head of HistoryPruneKeepBytes and sets the truncation flags the
// timeline explains. Every streamed column of a prunable kind maps to a flagged
// detail field (see streamedTextKeys in the HTTP DTO). It reports false when
// nothing needs shortening.
func prunedActivityPayload(row gen.SelectActivitiesForHistoryPruneRow) (gen.PruneActivityPayloadParams, int64, bool) {
	before := len(row.DetailJson) + len(row.CommandOutput) + len(row.StreamedText)
	if !prunableActivityKind(row.Kind) || before < historyPruneMinBytes {
		return gen.PruneActivityPayloadParams{}, 0, false
	}
	out := gen.PruneActivityPayloadParams{
		ID:                     row.ID,
		DetailJson:             row.DetailJson,
		CommandOutput:          row.CommandOutput,
		CommandOutputTruncated: row.CommandOutputTruncated,
		StreamedText:           row.StreamedText,
		StreamedTextTruncated:  row.StreamedTextTruncated,
	}
	if head, cut := textutil.HeadBytes(row.CommandOutput, HistoryPruneKeepBytes); cut {
		out.CommandOutput, out.CommandOutputTruncated = head, 1
	}
	if head, cut := textutil.HeadBytes(row.StreamedText, HistoryPruneKeepBytes); cut {
		out.StreamedText, out.StreamedTextTruncated = head, 1
	}
	if detail, ok := prunedActivityDetail(row.Kind, row.DetailJson, HistoryPruneKeepBytes); ok {
		out.DetailJson = detail
	}
	after := len(out.DetailJson) + len(out.CommandOutput) + len(out.StreamedText)
	if after >= before {
		return gen.PruneActivityPayloadParams{}, 0, false
	}
	return out, int64(before - after), true
}
