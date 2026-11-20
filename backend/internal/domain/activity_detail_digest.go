package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
)

// ActivityDetailOriginalDigestKey names the detail field a history prune
// writes when it shortens an activity's detail: the digest of the detail as it
// was before. Native-history reconciliation compares activities by detail, so a
// pruned row must still match a replay of the original.
const ActivityDetailOriginalDigestKey = "historyPrunedDetailSha256"

// ActivityDetailDigest identifies a stored activity's detail for comparison
// with a replay of it: the SHA-256 of its canonical form
// (canonicalActivityDetail), or, for a detail a history prune shortened, the
// digest it recorded of the original.
func ActivityDetailDigest(detail []byte) string {
	if bytes.Contains(detail, []byte(ActivityDetailOriginalDigestKey)) {
		var pruned map[string]json.RawMessage
		if json.Unmarshal(detail, &pruned) == nil {
			var digest string
			if json.Unmarshal(pruned[ActivityDetailOriginalDigestKey], &digest) == nil && digest != "" {
				return digest
			}
		}
	}
	return bytesDigest(canonicalActivityDetail(detail, false))
}

// ReplayActivityDetailDigests are the digests, in order of preference, under
// which a replayed detail may match a stored row's ActivityDetailDigest:
//
//   - its own ActivityDetailDigest, for a row stored with everything the
//     replay carries;
//   - the same without the ActivityDetailStreamShadowedKeys, for a row whose
//     output streamed into command_output and so was not kept in the detail
//     (B842). A provider replays the output it aggregated, which the stored row
//     no longer has to compare with;
//   - the plain SHA-256 of its bytes, which is what a history prune recorded
//     before digests were canonical, so a row pruned then still matches a
//     byte-identical replay.
func ReplayActivityDetailDigests(detail []byte) []string {
	digests := []string{ActivityDetailDigest(detail)}
	for _, digest := range []string{bytesDigest(canonicalActivityDetail(detail, true)), bytesDigest(detail)} {
		if !slices.Contains(digests, digest) {
			digests = append(digests, digest)
		}
	}
	return digests
}

func bytesDigest(detail []byte) string {
	sum := sha256.Sum256(detail)
	return hex.EncodeToString(sum[:])
}
