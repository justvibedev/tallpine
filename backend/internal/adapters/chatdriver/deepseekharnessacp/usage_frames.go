package deepseekharnessacp

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
)

var errBadFrame = errors.New("not a zstd frame")

// readFrame reads the next zstd frame from r, which must sit on a frame
// boundary. dsh appends each batch of events as its own frame, so a boundary is
// a safe place to resume without decompressing what came before. The frame
// format's headers and block sizes are walked (RFC 8878 §3.1.1) without
// decoding, so the caller learns the frame's length before holding it.
//
// consumed is the frame's byte length. frame holds its bytes unless it is longer
// than limit, in which case it is nil and the bytes were discarded rather than
// buffered. io.EOF means no bytes remained; io.ErrUnexpectedEOF means the frame
// is still being written (consumed is then not meaningful).
func readFrame(r *bufio.Reader, limit int) (frame []byte, consumed int64, err error) {
	var buf []byte
	keep := true
	var scratch [8]byte
	// field reads a header field of at most 8 bytes. It is always returned,
	// whether or not the frame is still being buffered.
	field := func(n int) ([]byte, error) {
		got := scratch[:n]
		_, err := io.ReadFull(r, got)
		consumed += int64(n)
		if err != nil {
			return nil, noEOF(err, consumed > int64(n))
		}
		if keep {
			if int64(len(buf))+int64(n) <= int64(limit) {
				buf = append(buf, got...)
			} else {
				keep, buf = false, nil
			}
		}
		return got, nil
	}
	// payload reads n bytes whose content is never inspected, buffering them only
	// while the frame is within limit.
	payload := func(n int) error {
		if keep && int64(len(buf))+int64(n) <= int64(limit) {
			start := len(buf)
			buf = append(buf, make([]byte, n)...)
			_, err := io.ReadFull(r, buf[start:])
			consumed += int64(n)
			return noEOF(err, true)
		}
		keep, buf = false, nil
		d, err := io.CopyN(io.Discard, r, int64(n))
		consumed += d
		return noEOF(err, true)
	}

	head, err := field(4)
	if err != nil {
		return nil, 0, err
	}
	magic := binary.LittleEndian.Uint32(head)
	if magic&0xFFFFFFF0 == 0x184D2A50 { // skippable frame
		size, err := field(4)
		if err != nil {
			return nil, 0, err
		}
		if err := payload(int(binary.LittleEndian.Uint32(size))); err != nil {
			return nil, 0, err
		}
		return buf, consumed, nil
	}
	if magic != 0xFD2FB528 {
		return nil, 0, errBadFrame
	}
	fhdb, err := field(1)
	if err != nil {
		return nil, 0, err
	}
	fhd := fhdb[0]
	hdr := 0
	single := fhd&0x20 != 0
	if !single {
		hdr++ // window descriptor
	}
	hdr += [4]int{0, 1, 2, 4}[fhd&3] // dictionary id
	switch fhd >> 6 {                // frame content size
	case 0:
		if single {
			hdr++
		}
	case 1:
		hdr += 2
	case 2:
		hdr += 4
	case 3:
		hdr += 8
	}
	for hdr > 0 { // descriptor, dictionary id and content size, at most 13 bytes
		n := min(hdr, 8)
		if _, err := field(n); err != nil {
			return nil, 0, err
		}
		hdr -= n
	}
	for {
		bh, err := field(3)
		if err != nil {
			return nil, 0, err
		}
		h := uint32(bh[0]) | uint32(bh[1])<<8 | uint32(bh[2])<<16
		last, kind, size := h&1 != 0, (h>>1)&3, int(h>>3)
		switch kind {
		case 1: // RLE: one byte stands for size repeats
			size = 1
		case 3:
			return nil, 0, errBadFrame
		}
		if err := payload(size); err != nil {
			return nil, 0, err
		}
		if last {
			break
		}
	}
	if fhd&4 != 0 { // content checksum
		if _, err := field(4); err != nil {
			return nil, 0, err
		}
	}
	return buf, consumed, nil
}

// noEOF reports a clean EOF in the middle of a frame as io.ErrUnexpectedEOF.
func noEOF(err error, mid bool) error {
	if errors.Is(err, io.EOF) && mid {
		return io.ErrUnexpectedEOF
	}
	return err
}
