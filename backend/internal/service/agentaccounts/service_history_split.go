package agentaccounts

import (
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// historySplit is the shared folders account keeps its own copy of, read
// without changing anything. Never nil, so the API shows an empty list.
func (s *Service) historySplit(account domain.AgentAccount) []string {
	if account.Device() {
		return []string{}
	}
	if probe, ok := s.providers[account.Provider].(ports.AgentAccountHistoryProbe); ok {
		if dirs := probe.HistorySplit(account.Home); len(dirs) > 0 {
			return dirs
		}
	}
	return []string{}
}

// warnSplitOnce logs a split account's history the first time a launch meets
// it. The launch goes on: binding and moves are what refuse, and a running
// agent must not die for it. Every launch would repeat the same line.
func (s *Service) warnSplitOnce(account domain.AgentAccount, err error) {
	var split *ports.SplitHistoryError
	if !errors.As(err, &split) {
		return
	}
	if _, seen := s.splitWarned.LoadOrStore(account.ID, struct{}{}); seen {
		return
	}
	s.log.Warn("agent account keeps its own history; conversations on other accounts cannot resume there",
		"account", account.ID, "home", split.Home, "dirs", split.Dirs)
}
