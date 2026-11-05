package kiro

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// TerminalModelUsage reads the credits a terminal-mode Kiro session has spent
// from Kiro's own transcript. Kiro reports no tokens, only credits per turn:
// every `usage_summary` record of
// <sessions root>/<workspace hash>/<session id>/messages.jsonl carries
// payload.promptTurnSummaries = [{unit: "credit", usage: <float>}]. The session
// id is the native id Tallpine's hooks recorded (with or without the `sess_`
// prefix of the directory). The context window fill comes from the last
// `session_metadata` record with key "contextUsage"
// (value.usagePercentage, already a 0-100 percentage). ok is false when no transcript is found.
func (p *Plugin) TerminalModelUsage(ctx context.Context, agentSessionID string) ([]domain.ModelUsage, bool, error) {
	agentSessionID = strings.TrimSpace(agentSessionID)
	if agentSessionID == "" {
		return nil, false, nil
	}
	root, err := p.kiroSessionsRoot()
	if err != nil {
		return nil, false, err
	}
	dir := findSessionDir(root, agentSessionID)
	if dir == "" {
		return nil, false, nil
	}
	file, err := os.Open(filepath.Join(dir, "messages.jsonl")) //nolint:gosec // path built from a sessions root and a matched directory
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()

	var credits float64
	var turns int64
	var contextPercent *float64
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		var line struct {
			Payload struct {
				Type    string              `json:"type"`
				Key     string              `json:"key"`
				Value   json.RawMessage     `json:"value"`
				Summary []PromptTurnSummary `json:"promptTurnSummaries"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		if line.Payload.Type == "session_metadata" && line.Payload.Key == "contextUsage" {
			// Each turn appends one; the last is the current fill.
			var v struct {
				Pct *float64 `json:"usagePercentage"`
			}
			if json.Unmarshal(line.Payload.Value, &v) == nil && v.Pct != nil {
				contextPercent = v.Pct
			}
			continue
		}
		if line.Payload.Type != "usage_summary" {
			continue
		}
		if turn, ok := CreditsFromSummaries(line.Payload.Summary); ok {
			credits += turn
			turns++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	model := "kiro"
	if raw, err := os.ReadFile(filepath.Join(dir, "session.json")); err == nil { //nolint:gosec // same directory as above
		var meta struct {
			ModelID string `json:"modelId"`
		}
		if json.Unmarshal(raw, &meta) == nil && meta.ModelID != "" {
			model = meta.ModelID
		}
	}
	return []domain.ModelUsage{{Model: model, Credits: credits, ContextPercent: contextPercent, Turns: turns}}, true, nil
}

// findSessionDir returns the transcript directory of the session, or "". The id
// comes from hook payloads, so it is matched by exact directory name (never as a
// glob pattern) and only when it is a plain identifier. When the same session
// directory exists under several workspace hashes the most recently written
// transcript wins, so the result does not depend on directory order.
// kiroSessionsRoot is ~/.kiro/sessions, where Kiro v3 keeps one directory per
// conversation for both `kiro-cli chat` and `kiro-cli acp`.
func (p *Plugin) kiroSessionsRoot() (string, error) {
	if p.sessionsRoot != "" {
		return p.sessionsRoot, nil
	}
	return defaultSessionsRoot()
}

func findSessionDir(root, agentSessionID string) string {
	return findSessionDirWith(root, agentSessionID, "messages.jsonl")
}

// findSessionDirWith is findSessionDir for any file Kiro writes into a session
// directory (messages.jsonl once a turn ran, session.json from session/new).
func findSessionDirWith(root, agentSessionID, file string) string {
	if !sessionIDPattern.MatchString(agentSessionID) {
		return ""
	}
	names := []string{agentSessionID, "sess_" + strings.TrimPrefix(agentSessionID, "sess_")}
	hashes, _ := os.ReadDir(root)
	best, bestTime := "", time.Time{}
	for _, name := range names {
		for _, hash := range hashes {
			if !hash.IsDir() {
				continue
			}
			dir := filepath.Join(root, hash.Name(), name)
			info, err := os.Stat(filepath.Join(dir, file))
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if best == "" || info.ModTime().After(bestTime) {
				best, bestTime = dir, info.ModTime()
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
