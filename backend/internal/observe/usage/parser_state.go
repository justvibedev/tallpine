package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const parserStateVersion = 1

const (
	maxCodexAttributionIDBytes = 256
	maxCodexAttributionIDs     = 4096
	integrityCheckpointBytes   = 4 << 10
)

type parserStateEnvelope struct {
	Version    int                     `json:"version"`
	SourceKind domain.UsageSourceKind  `json:"source_kind"`
	Integrity  *parserIntegrityStateV1 `json:"integrity,omitempty"`
	Claude     *claudeParserStateV1    `json:"claude,omitempty"`
	Codex      *codexParserStateV1     `json:"codex,omitempty"`
}

type parserIntegrityStateV1 struct {
	Checkpoint                *parserCheckpointV1 `json:"checkpoint,omitempty"`
	StableTail                *stableTailStateV1  `json:"stable_tail,omitempty"`
	DiscardingOversizedRecord bool                `json:"discarding_oversized_record,omitempty"`
}

type parserCheckpointV1 struct {
	EndOffset int64  `json:"end_offset"`
	ByteCount int64  `json:"byte_count"`
	SHA256    string `json:"sha256"`
}

type stableTailStateV1 struct {
	Offset            int64  `json:"offset"`
	ByteCount         int64  `json:"byte_count"`
	SHA256            string `json:"sha256"`
	QuietObservations int    `json:"quiet_observations"`
}

type claudeParserStateV1 struct {
	ModelID  string `json:"model_id,omitempty"`
	Provider string `json:"billing_provider,omitempty"`
	// LegacyProvider is the retired "provider" key. A build before #2928 filled
	// it with the harness name — "claude-code", "openai" — not a billing route,
	// so decoding must accept it (DisallowUnknownFields) and must never read
	// it. Reusing the key for the billing route would stamp the harness name
	// onto every event newly ingested from such a source, marked observed and
	// therefore beyond every repair path.
	LegacyProvider string `json:"provider,omitempty"`
}

type codexParserStateV1 struct {
	Baseline       codexTokenVector `json:"baseline"`
	ModelID        string           `json:"model_id,omitempty"`
	Provider       string           `json:"billing_provider,omitempty"`
	LegacyProvider string           `json:"provider,omitempty"` // see claudeParserStateV1

	NativeSessionID     string   `json:"native_session_id,omitempty"`
	DirectParentID      string   `json:"direct_parent_id,omitempty"`
	PendingSpawnCallIDs []string `json:"pending_spawn_call_ids"`
	DiscoveredChildIDs  []string `json:"discovered_child_ids"`
}

func decodeParserState(source domain.UsageSourceRecord) (*parserStateEnvelope, error) {
	raw := strings.TrimSpace(source.ParserStateJSON)
	if raw == "" || raw[0] != '{' {
		return nil, errors.New("state must be a JSON object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return nil, err
	}
	if len(object) == 0 {
		state, err := newParserState(source.Kind)
		if err != nil {
			return nil, err
		}
		if source.Kind == domain.UsageSourceCodexRollout {
			if err := validateCodexDirectParent(source, state.Codex); err != nil {
				return nil, err
			}
		}
		return state, nil
	}
	var state parserStateEnvelope
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, err
	}
	if state.Version != parserStateVersion {
		return nil, fmt.Errorf("unsupported version %d", state.Version)
	}
	if state.SourceKind != source.Kind {
		return nil, fmt.Errorf("source kind %q does not match %q", state.SourceKind, source.Kind)
	}
	if state.Integrity == nil {
		state.Integrity = &parserIntegrityStateV1{}
	}
	if err := validateParserIntegrityState(source, state.Integrity); err != nil {
		return nil, err
	}
	switch source.Kind {
	case domain.UsageSourceClaudeMain, domain.UsageSourceClaudeSubagent:
		if state.Claude == nil || state.Codex != nil {
			return nil, errors.New("claude state has invalid parser payload")
		}
		state.Claude.LegacyProvider = ""
	case domain.UsageSourceCodexRollout:
		if state.Codex == nil || state.Claude != nil {
			return nil, errors.New("codex state has invalid parser payload")
		}
		state.Codex.LegacyProvider = ""
		if state.Codex.PendingSpawnCallIDs == nil {
			state.Codex.PendingSpawnCallIDs = []string{}
		}
		if state.Codex.DiscoveredChildIDs == nil {
			state.Codex.DiscoveredChildIDs = []string{}
		}
		if err := normalizeCodexParserState(state.Codex); err != nil {
			return nil, err
		}
		if err := validateCodexDirectParent(source, state.Codex); err != nil {
			return nil, err
		}
	case domain.UsageSourceKimiWire:
		if state.Claude != nil || state.Codex != nil {
			return nil, errors.New("append-only state has invalid parser payload")
		}
	default:
		return nil, fmt.Errorf("unsupported source kind %q", source.Kind)
	}
	return &state, nil
}

func newParserState(kind domain.UsageSourceKind) (*parserStateEnvelope, error) {
	state := &parserStateEnvelope{
		Version:    parserStateVersion,
		SourceKind: kind,
		Integrity:  &parserIntegrityStateV1{},
	}
	switch kind {
	case domain.UsageSourceClaudeMain, domain.UsageSourceClaudeSubagent:
		state.Claude = &claudeParserStateV1{}
	case domain.UsageSourceCodexRollout:
		state.Codex = &codexParserStateV1{
			PendingSpawnCallIDs: []string{},
			DiscoveredChildIDs:  []string{},
		}
	case domain.UsageSourceKimiWire:
		// Kimi records carry stable native IDs, so no provider-specific
		// cumulative baseline is required.
	default:
		return nil, fmt.Errorf("unsupported source kind %q", kind)
	}
	return state, nil
}

func validateParserIntegrityState(source domain.UsageSourceRecord, state *parserIntegrityStateV1) error {
	if checkpoint := state.Checkpoint; checkpoint != nil {
		if checkpoint.EndOffset != source.ByteOffset ||
			checkpoint.ByteCount != min(checkpoint.EndOffset, int64(integrityCheckpointBytes)) ||
			!validSHA256Digest(checkpoint.SHA256) {
			return errors.New("invalid integrity checkpoint")
		}
	}
	if tail := state.StableTail; tail != nil {
		if tail.Offset != source.ByteOffset || tail.ByteCount <= 0 ||
			tail.QuietObservations < 0 || tail.QuietObservations > 1 || !validSHA256Digest(tail.SHA256) {
			return errors.New("invalid stable tail state")
		}
	}
	return nil
}

func validSHA256Digest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func normalizeCodexParserState(state *codexParserStateV1) error {
	pending, err := normalizeCodexIDs(state.PendingSpawnCallIDs, validCodexCallID)
	if err != nil {
		return fmt.Errorf("invalid pending spawn call ids: %w", err)
	}
	discovered, err := normalizeCodexIDs(state.DiscoveredChildIDs, validCodexAgentID)
	if err != nil {
		return fmt.Errorf("invalid discovered child ids: %w", err)
	}
	state.PendingSpawnCallIDs = pending
	state.DiscoveredChildIDs = discovered
	return nil
}
