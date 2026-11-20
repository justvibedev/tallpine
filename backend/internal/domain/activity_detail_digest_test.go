package domain

import (
	"slices"
	"testing"
)

// The stored digest ignores key order, encoding and a rawCommand that repeats
// command, but not output; a replay also offers the digest without its output
// for a row whose output streamed into command_output.
func TestActivityDetailDigestsCanonicalizeRepeats(t *testing.T) {
	goEncoded := []byte(`{"command":"a < b","exitCode":0,"rawCommand":"a < b"}`)
	sqliteEncoded := []byte(`{"exitCode":0,"command":"a < b"}`)
	if ActivityDetailDigest(goEncoded) != ActivityDetailDigest(sqliteEncoded) {
		t.Fatal("the same detail digested differently by encoding, key order or a repeated rawCommand")
	}
	wrapped := []byte(`{"command":"ls","rawCommand":"/bin/zsh -lc ls"}`)
	if ActivityDetailDigest(wrapped) == ActivityDetailDigest([]byte(`{"command":"ls"}`)) {
		t.Fatal("a rawCommand that differs from command was ignored")
	}
	withOutput := []byte(`{"command":"a < b","exitCode":0,"output":"x","outputSource":"aggregate"}`)
	if ActivityDetailDigest(withOutput) == ActivityDetailDigest(sqliteEncoded) {
		t.Fatal("the stored digest ignored output")
	}
	if !slices.Contains(ReplayActivityDetailDigests(withOutput), ActivityDetailDigest(sqliteEncoded)) {
		t.Fatal("a replay with output cannot match a row whose output streamed")
	}
	if got := ReplayActivityDetailDigests([]byte(`not json`)); len(got) != 1 || got[0] != bytesDigest([]byte(`not json`)) {
		t.Fatalf("non-JSON replay digests = %v, want the plain digest only", got)
	}
}
