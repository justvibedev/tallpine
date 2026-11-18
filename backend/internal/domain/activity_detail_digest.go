package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ActivityDetailOriginalDigestKey names the detail field a history prune
// writes when it shortens an activity's detail: the digest of the detail as it
// was before. Native-history reconciliation compares activities by detail, so a
// pruned row must still match a replay of the original.
const ActivityDetailOriginalDigestKey = "historyPrunedDetailSha256"

// ActivityDetailDigest identifies an activity's detail for exact comparison.
// It is the SHA-256 of the bytes, or, for a detail a history prune shortened,
// the digest it recorded of the original.
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
	sum := sha256.Sum256(detail)
	return hex.EncodeToString(sum[:])
}
