// Package conpty implements the detached PTY-host runtime used by Windows and
// macOS agent sessions. This file contains its OS-agnostic framing protocol.
//
// Frame layout: [1-byte type][4-byte big-endian length][payload]
package conpty

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	conPTYHostProtocolVersion         = 2
	conPTYStyledOutputProtocolVersion = 2
)

// Message type constants. Values must match pty-host.ts MSG_* constants exactly.
const (
	MsgTerminalData       byte = 0x01 // host -> client: raw PTY output
	MsgTerminalInput      byte = 0x02 // client -> host: raw keystrokes
	MsgResize             byte = 0x03 // client -> host: JSON {cols, rows}
	MsgGetOutputReq       byte = 0x04 // client -> host: JSON {lines}
	MsgGetOutputRes       byte = 0x05 // host -> client: UTF-8 text
	MsgStatusReq          byte = 0x06 // client -> host: empty
	MsgStatusRes          byte = 0x07 // host -> client: JSON {alive, pid, exitCode?}
	MsgKillReq            byte = 0x08 // client -> host: empty
	MsgGetStyledOutputReq byte = 0x09 // client -> host: JSON {lines}
	MsgGetStyledOutputRes byte = 0x0a // host -> client: rendered UTF-8 text with ANSI styles
	MsgAuth               byte = 0x0b // client -> host: per-host token; must be the first frame
)

// MaxFramePayload caps a frame's declared payload. The largest legitimate
// frame is a pasted-input chunk or an output snapshot; anything bigger is a
// misbehaving or hostile peer and the connection is dropped.
const MaxFramePayload = 1 << 20

// MaxHostFramePayload is the larger cap for frames a client reads from its own
// host (scrollback snapshots can hold 1000 arbitrarily long lines).
const MaxHostFramePayload = 64 << 20

// NewHostReplyParser is NewMessageParser with the cap sized for host replies.
func NewHostReplyParser(onMessage func(msgType byte, payload []byte)) *MessageParser {
	p := NewMessageParser(onMessage)
	p.maxPayload = MaxHostFramePayload
	return p
}

// ErrFrameTooLarge is reported by MessageParser when a header declares a
// payload over MaxFramePayload.
var ErrFrameTooLarge = errors.New("conpty: frame payload exceeds limit")

// JSON payload structs shared with later tasks (kept minimal).

// ResizePayload is the JSON body for MsgResize.
type ResizePayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// StatusPayload is the JSON body for MsgStatusRes.
type StatusPayload struct {
	Alive           bool `json:"alive"`
	PID             int  `json:"pid"`
	ExitCode        *int `json:"exitCode,omitempty"`
	ProtocolVersion int  `json:"protocolVersion,omitempty"`
}

// GetOutputReq is the JSON body for MsgGetOutputReq.
type GetOutputReq struct {
	Lines int `json:"lines"`
}

// EncodeMessage encodes a single frame into the binary protocol format.
// It allocates a fresh slice of exactly 5+len(payload) bytes.
// Returns an error if the payload exceeds the 4-byte length field capacity.
func EncodeMessage(msgType byte, payload []byte) ([]byte, error) {
	n := len(payload)
	if n > math.MaxUint32 {
		return nil, fmt.Errorf("conpty: payload too large (%d bytes, max %d)", n, math.MaxUint32)
	}
	payloadLen := uint32(n) // safe: n <= math.MaxUint32 checked above
	frame := make([]byte, 5+n)
	frame[0] = msgType
	binary.BigEndian.PutUint32(frame[1:5], payloadLen)
	copy(frame[5:], payload)
	return frame, nil
}

// MessageParser is a streaming parser for the binary framing protocol.
// It accumulates arbitrary-sized chunks from a pipe/socket stream and fires
// onMessage exactly once per complete frame, regardless of chunk boundaries.
// Safe to call Feed from a single goroutine; not concurrency-safe itself.
type MessageParser struct {
	buf        []byte
	err        error
	maxPayload uint32
	onMessage  func(msgType byte, payload []byte)
}

// NewMessageParser returns a parser that calls onMessage for each complete frame.
// onMessage receives a COPY of the payload so callers may retain it safely.
func NewMessageParser(onMessage func(msgType byte, payload []byte)) *MessageParser {
	return &MessageParser{onMessage: onMessage, maxPayload: MaxFramePayload}
}

// Feed appends chunk to the internal buffer and dispatches all complete frames.
// It matches the semantics of MessageParser.feed in pty-host.ts exactly:
// arbitrary chunk boundaries and multiple frames per chunk are both handled.
func (p *MessageParser) Feed(chunk []byte) {
	if p.err != nil {
		return
	}
	p.buf = append(p.buf, chunk...)

	for len(p.buf) >= 5 {
		payloadLen := binary.BigEndian.Uint32(p.buf[1:5])
		if payloadLen > p.maxPayload {
			p.err = ErrFrameTooLarge
			p.buf = nil
			return
		}
		frameLen := 5 + int(payloadLen)
		if len(p.buf) < frameLen {
			break
		}

		msgType := p.buf[0]
		// ponytail: explicit copy so callers that retain the slice are not
		// corrupted when p.buf grows/reallocates on a later Feed call.
		payload := make([]byte, payloadLen)
		copy(payload, p.buf[5:frameLen])

		p.buf = p.buf[frameLen:]
		p.onMessage(msgType, payload)
	}
}

// Err returns ErrFrameTooLarge once an oversize header was seen. The parser
// then ignores further input; the caller should close the connection.
func (p *MessageParser) Err() error { return p.err }
