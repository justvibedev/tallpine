package usage

import (
	"bytes"
	"context"
	"io"
	"os"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type jsonlChunk struct {
	records                   []jsonlRecord
	data                      []byte
	nextOffset                int64
	atEOF                     bool
	readToEOF                 bool
	fileSize                  int64
	trailing                  []byte
	trailingOffset            int64
	anomalies                 int
	errorCode                 string
	discardingOversizedRecord bool
}

func readJSONLChunkFromSnapshot(
	ctx context.Context,
	file *os.File,
	fileSize int64,
	offset int64,
	maxBytes int64,
	maxRecord int,
	discardingOversizedRecord bool,
) (jsonlChunk, error) {
	if err := ctx.Err(); err != nil {
		return jsonlChunk{}, err
	}
	if offset < 0 || fileSize < offset {
		return jsonlChunk{}, io.ErrUnexpectedEOF
	}
	readSize := min(maxBytes, fileSize-offset)
	if readSize < 0 {
		readSize = 0
	}
	data, err := io.ReadAll(contextReader{ctx: ctx, reader: io.NewSectionReader(file, offset, readSize)})
	if err != nil {
		return jsonlChunk{}, err
	}
	if int64(len(data)) != readSize {
		return jsonlChunk{}, io.ErrUnexpectedEOF
	}
	chunk := jsonlChunk{
		nextOffset: offset,
		data:       data,
		fileSize:   fileSize,
		readToEOF:  offset+int64(len(data)) >= fileSize,
	}
	if len(data) == 0 {
		chunk.atEOF = offset >= fileSize
		return chunk, nil
	}

	start := 0
	if discardingOversizedRecord {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			chunk.nextOffset += int64(len(data))
			chunk.errorCode = domain.UsageErrorRecordTooLarge
			chunk.discardingOversizedRecord = true
			return chunk, nil
		}
		start = newline + 1
		chunk.nextOffset += int64(start)
	}
	lastNewline := bytes.LastIndexByte(data[start:], '\n')
	if lastNewline < 0 {
		if len(data)-start > maxRecord {
			chunk.nextOffset += int64(len(data) - start)
			chunk.anomalies++
			chunk.errorCode = domain.UsageErrorRecordTooLarge
			chunk.discardingOversizedRecord = true
			chunk.atEOF = chunk.nextOffset >= fileSize
		} else {
			chunk.trailing = append([]byte(nil), data[start:]...)
			chunk.trailingOffset = offset + int64(start)
		}
		return chunk, nil
	}
	completeEnd := start + lastNewline + 1
	cursor := start
	for cursor < completeEnd {
		if err := ctx.Err(); err != nil {
			return jsonlChunk{}, err
		}
		relative := bytes.IndexByte(data[cursor:completeEnd], '\n')
		if relative < 0 {
			break
		}
		end := cursor + relative
		line := bytes.TrimSuffix(data[cursor:end], []byte{'\r'})
		lineOffset := offset + int64(cursor)
		if len(line) > maxRecord {
			chunk.anomalies++
			chunk.errorCode = domain.UsageErrorRecordTooLarge
		} else if len(bytes.TrimSpace(line)) > 0 {
			chunk.records = append(chunk.records, jsonlRecord{Data: append([]byte(nil), line...), Offset: lineOffset})
		}
		cursor = end + 1
	}
	chunk.nextOffset = offset + int64(completeEnd)
	chunk.atEOF = chunk.nextOffset >= fileSize
	if !chunk.atEOF && completeEnd < len(data) && len(data)-completeEnd <= maxRecord {
		chunk.trailing = append([]byte(nil), data[completeEnd:]...)
		chunk.trailingOffset = chunk.nextOffset
	}
	return chunk, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
