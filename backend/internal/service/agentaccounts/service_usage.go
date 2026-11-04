package agentaccounts

import (
	"context"
	"sort"
	"time"
)

// AccountUsage is one account's model usage in Tallpine over a period.
type AccountUsage struct {
	AccountID    string
	Days         []DayUsage
	Models       []ModelUsage
	InputTokens  int64
	OutputTokens int64
	CostNanos    int64
}

// DayUsage is one UTC day of an account's usage.
type DayUsage struct {
	Day       string
	Tokens    int64
	CostNanos int64
}

// ModelUsage is one model's share of an account's usage.
type ModelUsage struct {
	Model     string
	Tokens    int64
	CostNanos int64
}

// Usage sums each account's usage in Tallpine sessions over the last days, from Tallpine's
// own reading of the session logs.
func (s *Service) Usage(ctx context.Context, days int) ([]AccountUsage, error) {
	if days <= 0 || days > 90 {
		days = 30
	}
	since := s.now().AddDate(0, 0, -days+1).Truncate(24 * time.Hour)
	rows, err := s.store.AgentAccountUsageSince(ctx, since)
	if err != nil {
		return nil, err
	}
	byAccount := map[string]*AccountUsage{}
	dayIndex := map[string]map[string]int{}
	modelIndex := map[string]map[string]int{}
	for _, row := range rows {
		u := byAccount[row.AccountID]
		if u == nil {
			u = &AccountUsage{AccountID: row.AccountID}
			byAccount[row.AccountID] = u
			dayIndex[row.AccountID], modelIndex[row.AccountID] = map[string]int{}, map[string]int{}
		}
		tokens := row.InputTokens + row.OutputTokens
		u.InputTokens += row.InputTokens
		u.OutputTokens += row.OutputTokens
		u.CostNanos += row.CostNanos
		if i, ok := dayIndex[row.AccountID][row.Day]; ok {
			u.Days[i].Tokens += tokens
			u.Days[i].CostNanos += row.CostNanos
		} else {
			dayIndex[row.AccountID][row.Day] = len(u.Days)
			u.Days = append(u.Days, DayUsage{Day: row.Day, Tokens: tokens, CostNanos: row.CostNanos})
		}
		if i, ok := modelIndex[row.AccountID][row.Model]; ok {
			u.Models[i].Tokens += tokens
			u.Models[i].CostNanos += row.CostNanos
		} else {
			modelIndex[row.AccountID][row.Model] = len(u.Models)
			u.Models = append(u.Models, ModelUsage{Model: row.Model, Tokens: tokens, CostNanos: row.CostNanos})
		}
	}
	out := make([]AccountUsage, 0, len(byAccount))
	for _, u := range byAccount {
		sort.Slice(u.Models, func(i, j int) bool { return u.Models[i].Tokens > u.Models[j].Tokens })
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out, nil
}
