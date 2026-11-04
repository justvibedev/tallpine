package codexappserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// ListModels asks the provider which models this account may use.
//
// The provider is the only honest source: models get added, renamed, hidden per
// account, and gated by entitlement that Tallpine cannot see. A table in Tallpine would be
// wrong within a week.
func (c *conversation) ListModels(ctx context.Context) ([]ports.ChatModel, error) {
	models, err := listModels(ctx, c.conn)
	if err != nil {
		return nil, err
	}
	fastTiers := make(map[string]string, len(models))
	for _, model := range models {
		if model.FastTier != "" {
			fastTiers[model.ID] = model.FastTier
		}
	}
	c.sendMu.Lock()
	c.fastTiers = fastTiers
	threadModel, threadEffort := c.threadModel, c.threadEffort
	c.sendMu.Unlock()
	// Thread settings include the user's config; model/list only has generic defaults.
	for i := range models {
		// An omitted turn model inherits thread/start (including config.toml),
		// not model/list's generic catalog default. If the configured model is
		// absent, leave no catalog default rather than advertise another model.
		if threadModel != "" {
			models[i].Default = models[i].ID == threadModel
		}
		if models[i].ID == threadModel && threadEffort != "" {
			models[i].DefaultEffort = threadEffort
		}
	}
	return models, nil
}

func listModels(ctx context.Context, connection *conn) ([]ports.ChatModel, error) {
	type modelListResponse struct {
		Data []struct {
			ID          string   `json:"id"`
			Model       string   `json:"model"`
			DisplayName string   `json:"displayName"`
			Description string   `json:"description"`
			IsDefault   bool     `json:"isDefault"`
			Hidden      bool     `json:"hidden"`
			DefaultEff  string   `json:"defaultReasoningEffort"`
			Inputs      []string `json:"inputModalities"`
			Efforts     []struct {
				ReasoningEffort string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
			ServiceTiers []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"serviceTiers"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}

	var models []ports.ChatModel
	var cursor string
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var resp modelListResponse
		if err := connection.request(ctx, "model/list", params, &resp); err != nil {
			return nil, fmt.Errorf("model/list: %w", err)
		}
		for _, entry := range resp.Data {
			if entry.Hidden {
				// The provider marks a model hidden when the account should not be
				// offered it. Showing it anyway would offer a choice that then fails.
				continue
			}
			id := entry.ID
			if id == "" {
				id = entry.Model
			}
			if id == "" {
				continue
			}
			efforts := make([]string, 0, len(entry.Efforts))
			for _, effort := range entry.Efforts {
				if effort.ReasoningEffort != "" {
					efforts = append(efforts, effort.ReasoningEffort)
				}
			}
			display := entry.DisplayName
			if display == "" {
				display = id
			}
			// The faster tier is named "Fast" and ids "priority"; the name identifies it.
			fastTier := ""
			for _, tier := range entry.ServiceTiers {
				if strings.EqualFold(tier.Name, "fast") || tier.ID == "fast" {
					fastTier = tier.ID
					break
				}
			}
			models = append(models, ports.ChatModel{
				ID: id, DisplayName: display, Description: entry.Description,
				Default: entry.IsDefault, Efforts: efforts, DefaultEffort: entry.DefaultEff,
				FastTier: fastTier,
				Inputs:   entry.Inputs,
			})
		}
		if resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		cursor = *resp.NextCursor
	}
	return models, nil
}

// ReadRateLimits asks the provider where the account stands right now.
//
// The provider also pushes account/rateLimits/updated, but only alongside a turn.
// That is too late for the question this answers: a user opening a conversation
// wants to know whether they have quota BEFORE spending a turn finding out. The
// controller reads once at startup for exactly that reason.
func (c *conversation) ReadRateLimits(ctx context.Context) (ports.ChatRateLimits, error) {
	var resp capacityReadEnvelope
	if err := c.conn.request(ctx, "account/rateLimits/read", map[string]any{}, &resp); err != nil {
		return ports.ChatRateLimits{}, fmt.Errorf("account/rateLimits/read: %w", err)
	}
	observedAt := time.Now().UTC()
	return chatRateLimitsFromCapacity(capacityObservationFromEnvelope(resp, observedAt, false), observedAt), nil
}
