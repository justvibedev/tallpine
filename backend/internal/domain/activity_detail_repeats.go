package domain

import (
	"bytes"
	"encoding/json"
)

// ActivityDetailStreamShadowedKeys are the command detail keys the read path
// sets or removes itself whenever the activity's command_output column holds
// streamed output (activityDetailPayload in httpd/controllers). A stored copy
// of any of them is never shown, so the store does not keep one (B842).
var ActivityDetailStreamShadowedKeys = []string{"output", "outputTruncated", "outputSource", "outputMayBePartial"}

// ActivityDetailMayRepeat reports, without decoding, whether detail could hold
// something DropActivityDetailRepeats removes, so most details skip the decode.
func ActivityDetailMayRepeat(detail []byte, outputStreamed bool) bool {
	return bytes.Contains(detail, []byte(`"rawCommand"`)) ||
		(outputStreamed && bytes.Contains(detail, []byte(`"output`)))
}

// DropActivityDetailRepeats removes what a top-level activity detail repeats
// and reports whether anything was removed: with outputStreamed the
// ActivityDetailStreamShadowedKeys, and always a rawCommand whose value equals
// command. Every reader of rawCommand falls back to command; a rawCommand that
// differs (a provider wrapped the command in a shell invocation) is the audit
// record of what ran and stays.
func DropActivityDetailRepeats(fields map[string]json.RawMessage, outputStreamed bool) bool {
	changed := false
	if outputStreamed {
		for _, key := range ActivityDetailStreamShadowedKeys {
			if _, ok := fields[key]; ok {
				delete(fields, key)
				changed = true
			}
		}
	}
	if sameJSONString(fields["rawCommand"], fields["command"]) {
		delete(fields, "rawCommand")
		changed = true
	}
	return changed
}

func sameJSONString(a, b json.RawMessage) bool {
	var left, right string
	if a == nil || b == nil || json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return left == right
}

// canonicalActivityDetail is the form activity detail digests hash: the
// detail without a rawCommand that repeats command and, with dropOutput, without
// the ActivityDetailStreamShadowedKeys, re-encoded with sorted keys and numbers
// kept as written, so the same JSON stored by the Go encoder or by SQLite's
// json_remove hashes alike. A detail that is not a JSON object is hashed as it
// is.
func canonicalActivityDetail(detail []byte, dropOutput bool) []byte {
	var fields map[string]json.RawMessage
	if json.Unmarshal(detail, &fields) != nil || fields == nil {
		return detail
	}
	DropActivityDetailRepeats(fields, dropOutput)
	encoded, err := json.Marshal(fields)
	if err != nil {
		return detail
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return detail
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return detail
	}
	return canonical
}
