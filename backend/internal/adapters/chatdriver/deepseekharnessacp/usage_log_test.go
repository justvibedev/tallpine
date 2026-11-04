package deepseekharnessacp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// record builds an assistant/message line in dsh's real v4 shape: the request's
// usage at the top level and again inside a stream chunk.
func record(t *testing.T, seq int64, model string, at time.Time, miss, hit, out int64) string {
	t.Helper()
	usage := map[string]any{"inputTokens": miss, "outputTokens": out, "cacheReadTokens": hit, "cacheWriteTokens": 0, "totalTokens": miss + hit + out}
	b, err := json.Marshal(map[string]any{
		"type": "assistant/message", "seq": seq, "time": at.UnixMilli(),
		"data": map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{"role": "assistant", "source": map[string]any{"kind": "model", "provider": "deepseek-official", "model": model}},
			"usage":   usage,
			"stream":  []any{map[string]any{"type": "chunk", "chunk": map[string]any{"type": "usage", "usage": usage}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

const testSessionID = "558667c7-e2de-4ae8-90a2-b89212aeb4fa"

// frame compresses lines as one independent zstd frame, the way dsh appends a
// batch of events.
func frame(t *testing.T, lines ...string) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	return enc.EncodeAll([]byte(strings.Join(lines, "")), nil)
}

// noise are records that carry no usage; they and a malformed line are skipped.
var noise = []string{`{"type":"session","version":4}` + "\n", "not json\n", `{"type":"turn/start","seq":4}` + "\n"}

func logPath(root string) string {
	return filepath.Join(root, "--some-cwd--", testSessionID, "session.v4.jsonl.zstd")
}

// writeLogAt writes the session log with one frame per batch.
func writeLogAt(t *testing.T, root string, batches ...[]string) string {
	t.Helper()
	path := logPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var all []byte
	for _, b := range batches {
		all = append(all, frame(t, b...)...)
	}
	if err := os.WriteFile(path, all, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendBytes(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
}

// readUsage totals a log with a fresh ledger.
func readUsage(t *testing.T, path string) *ports.ChatUsage {
	t.Helper()
	var l ledger
	if err := l.refresh(path, nil); err != nil {
		t.Fatal(err)
	}
	return l.usage()
}

var offPeak = time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC)

func TestReadSessionUsageMapsMissHitAndOutput(t *testing.T) {
	// Values taken from a real dsh request: 9471 miss, 10496 hit, 202 output.
	path := writeLogAt(t, t.TempDir(), append(noise, record(t, 15, "deepseek-flash", offPeak, 9471, 10496, 202)))
	u := readUsage(t, path)
	if u == nil || u.InputTokens != 9471 || u.CachedTokens != 10496 || u.OutputTokens != 202 || u.TotalTokens != 20169 || !u.TotalsKnown {
		t.Fatalf("mapping: %+v", u)
	}
	want := (9471*0.15 + 10496*0.003 + 202*0.60) / 1e6
	if u.Cost == nil || u.Currency != "USD" || math.Abs(*u.Cost-want) > 1e-12 {
		t.Fatalf("cost: %v want %v", u.Cost, want)
	}
}

func TestReadSessionUsageDoesNotDoubleCount(t *testing.T) {
	line := record(t, 15, "deepseek-flash", offPeak, 100, 200, 10)
	// The request appears at top level and in its stream chunk (see record), and
	// the whole record is repeated: counted once.
	path := writeLogAt(t, t.TempDir(), []string{line, line, record(t, 26, "deepseek-flash", offPeak, 1, 2, 3)})
	u := readUsage(t, path)
	if u == nil || u.InputTokens != 101 || u.CachedTokens != 202 || u.OutputTokens != 13 || u.TotalTokens != 316 {
		t.Fatalf("double counted: %+v", u)
	}
}

func TestReadSessionUsagePricesEachRequestByItsOwnTime(t *testing.T) {
	path := writeLogAt(t, t.TempDir(), []string{
		record(t, 1, "deepseek-flash", time.Date(2026, 10, 26, 2, 0, 0, 0, time.UTC), 1e6, 0, 0), // peak
		record(t, 2, "deepseek-flash", offPeak, 1e6, 0, 0),
	})
	u := readUsage(t, path)
	if u == nil || u.Cost == nil || math.Abs(*u.Cost-(0.30+0.15)) > 1e-9 {
		t.Fatalf("cost: %+v", u)
	}
}

func TestReadSessionUsageUnknownModelKeepsTokensDropsCost(t *testing.T) {
	path := writeLogAt(t, t.TempDir(), []string{record(t, 1, "deepseek-v4-pro", offPeak, 10, 20, 30), record(t, 2, "deepseek-flash", offPeak, 1, 1, 1)})
	u := readUsage(t, path)
	if u == nil || u.TotalTokens != 63 || u.Cost != nil || u.Currency != "" || !u.CostUnknown {
		t.Fatalf("unknown model: %+v", u)
	}
}

// A model switch mid-session: each request keeps the price of its own model,
// and a later unpriced request withdraws the cost rather than leaving the old
// figure standing.
func TestMixedModelSessionPricesEachRequestByItsOwnModel(t *testing.T) {
	offPeakPrices["deepseek-test"] = price{cacheHit: 1, cacheMiss: 10, output: 100}
	t.Cleanup(func() { delete(offPeakPrices, "deepseek-test") })

	path := writeLogAt(t, t.TempDir(), []string{record(t, 1, "deepseek-flash", offPeak, 1e6, 0, 0)})
	var l ledger
	if err := l.refresh(path, nil); err != nil {
		t.Fatal(err)
	}
	if u := l.usage(); u.Cost == nil || math.Abs(*u.Cost-0.15) > 1e-9 {
		t.Fatalf("flash cost: %+v", u)
	}
	appendBytes(t, path, frame(t, record(t, 2, "deepseek-test", offPeak, 1e6, 0, 0)))
	if err := l.refresh(path, nil); err != nil {
		t.Fatal(err)
	}
	if u := l.usage(); u.Cost == nil || math.Abs(*u.Cost-(0.15+10)) > 1e-9 {
		t.Fatalf("mixed cost should be 0.15 (flash) + 10 (test), got %+v", u)
	}
	appendBytes(t, path, frame(t, record(t, 3, "deepseek-v4-pro", offPeak, 5, 0, 0)))
	if err := l.refresh(path, nil); err != nil {
		t.Fatal(err)
	}
	if u := l.usage(); u.Cost != nil || !u.CostUnknown || u.InputTokens != 2e6+5 {
		t.Fatalf("unpriced request must withdraw cost, keep tokens: %+v", u)
	}
}

// A long session must not be decompressed again each turn: the ledger resumes at
// the compressed offset it already processed. Garbling that already-read prefix
// (same length, same file) proves it is not read a second time.
func TestLedgerReadsOnlyNewFrames(t *testing.T) {
	first := frame(t, record(t, 1, "deepseek-flash", offPeak, 100, 0, 1))
	path := writeLogAt(t, t.TempDir(), []string{record(t, 1, "deepseek-flash", offPeak, 100, 0, 1)})
	var l ledger
	if err := l.refresh(path, nil); err != nil {
		t.Fatal(err)
	}
	if l.offset != int64(len(first)) {
		t.Fatalf("offset = %d, want %d", l.offset, len(first))
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(make([]byte, len(first)), 0); err != nil {
		t.Fatal(err)
	}
	f.Close()
	appendBytes(t, path, frame(t, record(t, 2, "deepseek-flash", offPeak, 50, 0, 2)))
	if err := l.refresh(path, nil); err != nil {
		t.Fatalf("re-read the processed prefix: %v", err)
	}
	if u := l.usage(); u.InputTokens != 150 || u.OutputTokens != 3 {
		t.Fatalf("totals after incremental refresh: %+v", u)
	}
}

// A frame dsh is still writing is left alone until it completes.
func TestLedgerWaitsForTornTailFrame(t *testing.T) {
	path := writeLogAt(t, t.TempDir(), []string{record(t, 1, "deepseek-flash", offPeak, 100, 0, 0)})
	var l ledger
	if err := l.refresh(path, nil); err != nil {
		t.Fatal(err)
	}
	second := frame(t, record(t, 2, "deepseek-flash", offPeak, 7, 0, 0))
	appendBytes(t, path, second[:len(second)/2])
	if err := l.refresh(path, nil); err != nil || l.usage().InputTokens != 100 {
		t.Fatalf("torn tail counted or errored: err=%v %+v", err, l.usage())
	}
	appendBytes(t, path, second[len(second)/2:])
	if err := l.refresh(path, nil); err != nil || l.usage().InputTokens != 107 {
		t.Fatalf("completed frame not counted: err=%v %+v", err, l.usage())
	}
}

// A replaced file (rotation, migration) or a shrunk one starts the count over.
func TestLedgerStartsOverOnRotation(t *testing.T) {
	root := t.TempDir()
	path := writeLogAt(t, root, []string{record(t, 1, "deepseek-flash", offPeak, 1000, 0, 0), record(t, 2, "deepseek-flash", offPeak, 1000, 0, 0)})
	var l ledger
	if err := l.refresh(path, nil); err != nil || l.usage().InputTokens != 2000 {
		t.Fatalf("err=%v %+v", err, l.usage())
	}
	// Replace with a different, smaller file whose seqs restart.
	next := path + ".new"
	if err := os.WriteFile(next, frame(t, record(t, 1, "deepseek-flash", offPeak, 5, 0, 0)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, path); err != nil {
		t.Fatal(err)
	}
	if err := l.refresh(path, nil); err != nil || l.usage().InputTokens != 5 {
		t.Fatalf("rotation not detected: err=%v %+v", err, l.usage())
	}
	// Same inode truncated below the offset.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.refresh(path, nil); err != nil || l.usage() != nil {
		t.Fatalf("truncation not detected: err=%v %+v", err, l.usage())
	}
}

func TestTrackerKeepsSeparateLedgerPerSessionAndFindsLaunchHome(t *testing.T) {
	home := t.TempDir()
	writeLogAt(t, filepath.Join(home, "sessions"), []string{record(t, 1, "deepseek-flash", offPeak, 10, 20, 5)})
	other := "11111111-2222-3333-4444-555555555555"
	otherPath := filepath.Join(home, "sessions", "--cwd--", other, "session.v4.jsonl.zstd")
	if err := os.MkdirAll(filepath.Dir(otherPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, frame(t, record(t, 1, "deepseek-flash", offPeak, 99, 0, 0)), 0o600); err != nil {
		t.Fatal(err)
	}

	tr := newUsageTracker(nil)
	if tr.sessionUsage(testSessionID) != nil {
		t.Fatal("log outside every known root was found")
	}
	tr.noteLaunch(map[string]string{"DSH_HOME": home}) // the env the launch gave dsh
	a, b := tr.sessionUsage(testSessionID), tr.sessionUsage(other)
	if a == nil || a.InputTokens != 10 || b == nil || b.InputTokens != 99 {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	if tr.sessionUsage("../x") != nil {
		t.Fatal("path-like id accepted")
	}
}

// The size bounds are variables so tests can lower them.
func lowerBounds(t *testing.T, frameBytes int, decodedBytes uint64) {
	t.Helper()
	oldF, oldD := maxFrameBytes, maxDecodedBytes
	maxFrameBytes, maxDecodedBytes = frameBytes, decodedBytes
	t.Cleanup(func() { maxFrameBytes, maxDecodedBytes = oldF, oldD })
}

// A log far larger than the frame cap is read in full, one bounded frame at a
// time, and a single frame over the cap is skipped unread (never buffered).
func TestLedgerReadsLogLargerThanCapAndSkipsOversizeFrame(t *testing.T) {
	lowerBounds(t, 4<<10, maxDecodedBytes)
	var batches [][]string
	for i := int64(1); i <= 40; i++ {
		batches = append(batches, []string{record(t, i, "deepseek-flash", offPeak, 10, 0, 0)})
	}
	// Incompressible filler makes one frame exceed the 4 KiB cap.
	filler := make([]byte, 16<<10)
	if _, err := rand.Read(filler); err != nil {
		t.Fatal(err)
	}
	// The oversize frame also holds a usage record; skipped unread, it is not counted.
	big := []string{`{"type":"noise","x":"` + hex.EncodeToString(filler) + `"}` + "\n", record(t, 999, "deepseek-flash", offPeak, 7777, 0, 0)}
	batches = append(batches[:20], append([][]string{big}, batches[20:]...)...)
	path := writeLogAt(t, t.TempDir(), batches...)
	st, _ := os.Stat(path)
	if st.Size() <= int64(maxFrameBytes) {
		t.Fatalf("fixture (%d bytes) must exceed the cap", st.Size())
	}
	var l ledger
	if err := l.refresh(path, nil); err != nil {
		t.Fatal(err)
	}
	if u := l.usage(); u == nil || u.InputTokens != 400 || l.offset != st.Size() {
		t.Fatalf("usage=%+v offset=%d size=%d", u, l.offset, st.Size())
	}
}

// A small frame that expands past the decoder limit is skipped and the offset
// advances past it, so the next read neither resets nor loops on it.
func TestLedgerSkipsDecompressionBombFrame(t *testing.T) {
	lowerBounds(t, maxFrameBytes, 1<<20)
	// A usage record inside the bomb is lost with the skipped frame; the bounds
	// matter more than one frame's accounting.
	bomb := []string{`{"type":"noise","x":"` + strings.Repeat("a", 8<<20) + `"}` + "\n", record(t, 998, "deepseek-flash", offPeak, 7777, 0, 0)}
	path := writeLogAt(t, t.TempDir(),
		[]string{record(t, 1, "deepseek-flash", offPeak, 10, 0, 0)}, bomb,
		[]string{record(t, 2, "deepseek-flash", offPeak, 5, 0, 0)})
	st, _ := os.Stat(path)
	var l ledger
	if err := l.refresh(path, nil); err != nil {
		t.Fatal(err)
	}
	if u := l.usage(); u == nil || u.InputTokens != 15 || l.offset != st.Size() {
		t.Fatalf("bomb not skipped cleanly: usage=%+v offset=%d size=%d", u, l.offset, st.Size())
	}
	if err := l.refresh(path, nil); err != nil || l.usage().InputTokens != 15 {
		t.Fatalf("second read: err=%v %+v", err, l.usage())
	}
}

func TestTrackerRejectsGlobMetacharactersInSessionID(t *testing.T) {
	home := t.TempDir()
	writeLogAt(t, filepath.Join(home, "sessions"), []string{record(t, 1, "deepseek-flash", offPeak, 10, 0, 0)})
	tr := newUsageTracker(nil)
	tr.noteLaunch(map[string]string{"DSH_HOME": home})
	for _, id := range []string{"*", "?", "[a-z]*", "5586*"} {
		if tr.sessionUsage(id) != nil {
			t.Fatalf("id %q matched a log", id)
		}
	}
}

// An oversize frame made of many blocks (real frames over the cap always are:
// blocks are at most 128 KiB) is skipped without panicking, and the frames after
// it are still counted.
func TestLedgerSkipsMultiBlockOversizeFrame(t *testing.T) {
	lowerBounds(t, 4<<10, maxDecodedBytes)
	filler := make([]byte, 600<<10)
	if _, err := rand.Read(filler); err != nil {
		t.Fatal(err)
	}
	big := []string{`{"type":"noise","x":"` + hex.EncodeToString(filler) + `"}` + "\n"}
	path := writeLogAt(t, t.TempDir(),
		[]string{record(t, 1, "deepseek-flash", offPeak, 10, 0, 0)}, big,
		[]string{record(t, 2, "deepseek-flash", offPeak, 5, 0, 0)})
	st, _ := os.Stat(path)
	var l ledger
	if err := l.refresh(path, nil); err != nil {
		t.Fatal(err)
	}
	if u := l.usage(); u == nil || u.InputTokens != 15 || l.offset != st.Size() {
		t.Fatalf("usage=%+v offset=%d size=%d", u, l.offset, st.Size())
	}
}

// Unreadable framing resets the ledger so a file rewritten in place does not
// freeze the old totals.
func TestLedgerResetsOnBadFraming(t *testing.T) {
	path := writeLogAt(t, t.TempDir(), []string{record(t, 1, "deepseek-flash", offPeak, 10, 0, 0)})
	var l ledger
	if err := l.refresh(path, nil); err != nil || l.usage().InputTokens != 10 {
		t.Fatalf("err=%v", err)
	}
	appendBytes(t, path, []byte("garbage that is not a zstd frame"))
	if err := l.refresh(path, nil); err == nil {
		t.Fatal("bad framing not reported")
	}
	if l.offset != 0 || l.lastSeq != 0 || l.usage() != nil {
		t.Fatalf("ledger not reset: %+v", l)
	}
}
