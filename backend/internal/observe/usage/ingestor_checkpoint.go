package usage

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	usagesvc "tallpine.eu/tallpine/backend/internal/service/usage"
)

func codexChunkAttributionMatches(
	source domain.UsageSourceRecord,
	state *codexParserStateV1,
	records []jsonlRecord,
) bool {
	if source.Kind != domain.UsageSourceCodexRollout || source.ByteOffset != 0 || state == nil {
		return true
	}
	requireAttribution := source.SubagentID != "" || state.NativeSessionID != ""
	if !requireAttribution || len(records) == 0 {
		return true
	}
	nativeSessionID, directParentID, ok := codexSessionMetaFromRecord(records[0].Data)
	if !ok || nativeSessionID != source.NativeSessionID {
		return false
	}
	if source.SubagentID == "" {
		return directParentID == ""
	}
	return source.SubagentID == source.NativeSessionID && directParentID == state.DirectParentID
}

func stableTailObservation(offset int64, tail []byte) *stableTailStateV1 {
	digest := sha256.Sum256(tail)
	return &stableTailStateV1{
		Offset:    offset,
		ByteCount: int64(len(tail)),
		SHA256:    fmt.Sprintf("%x", digest),
	}
}

func sameStableTail(left, right *stableTailStateV1) bool {
	return left != nil && right != nil &&
		left.Offset == right.Offset &&
		left.ByteCount == right.ByteCount &&
		left.SHA256 == right.SHA256
}

func nextFinalizationRetry(
	now time.Time,
	wait time.Duration,
	existing *time.Time,
	restart bool,
) time.Time {
	if !restart && existing != nil && existing.After(now) {
		return existing.UTC()
	}
	return now.Add(wait)
}

type transcriptSnapshot struct {
	identity     string
	size         int64
	modTime      time.Time
	cursorSample parserCheckpointSample
}

type parserCheckpointSample struct {
	checkpoint *parserCheckpointV1
	data       []byte
}

func parserCheckpointSampleAt(file *os.File, offset int64) (parserCheckpointSample, error) {
	if offset <= 0 {
		return parserCheckpointSample{}, nil
	}
	byteCount := min(offset, int64(integrityCheckpointBytes))
	data := make([]byte, byteCount)
	read, err := file.ReadAt(data, offset-byteCount)
	if err != nil && !errors.Is(err, io.EOF) {
		return parserCheckpointSample{}, err
	}
	if int64(read) != byteCount {
		return parserCheckpointSample{}, io.ErrUnexpectedEOF
	}
	digest := sha256.Sum256(data)
	return parserCheckpointSample{
		checkpoint: &parserCheckpointV1{
			EndOffset: offset,
			ByteCount: byteCount,
			SHA256:    fmt.Sprintf("%x", digest),
		},
		data: data,
	}, nil
}

func parserCheckpointsEqual(left, right *parserCheckpointV1) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.EndOffset == right.EndOffset &&
		left.ByteCount == right.ByteCount &&
		left.SHA256 == right.SHA256
}

func parserCheckpointAfterRead(
	before parserCheckpointSample,
	offset int64,
	nextOffset int64,
	read []byte,
) (*parserCheckpointV1, error) {
	consumed := nextOffset - offset
	if consumed < 0 || consumed > int64(len(read)) {
		return nil, io.ErrUnexpectedEOF
	}
	if nextOffset <= 0 {
		return nil, nil
	}
	window := make([]byte, 0, len(before.data)+int(consumed))
	window = append(window, before.data...)
	window = append(window, read[:consumed]...)
	byteCount := min(nextOffset, int64(integrityCheckpointBytes))
	if int64(len(window)) < byteCount {
		return nil, io.ErrUnexpectedEOF
	}
	window = window[len(window)-int(byteCount):]
	digest := sha256.Sum256(window)
	return &parserCheckpointV1{
		EndOffset: nextOffset,
		ByteCount: byteCount,
		SHA256:    fmt.Sprintf("%x", digest),
	}, nil
}

func transcriptSnapshotUnchanged(file *os.File, offset int64, before transcriptSnapshot) (bool, error) {
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	identity, err := usagesvc.SourceIdentityFromFile(file)
	if err != nil {
		return false, err
	}
	afterCursor, err := parserCheckpointSampleAt(file, offset)
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular() &&
		identity == before.identity &&
		info.Size() == before.size &&
		info.ModTime().Equal(before.modTime) &&
		parserCheckpointsEqual(afterCursor.checkpoint, before.cursorSample.checkpoint), nil
}
