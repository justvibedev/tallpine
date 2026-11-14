// Package ownermsg checks that an orchestrator's decision rests on a message
// the owner really typed: the proof plan approval and ask answers share.
package ownermsg

import (
	"context"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Source is the message lookup the check needs; the SQLite store satisfies it.
type Source interface {
	BacklogPlanMessageSource(ctx context.Context, messageID string) (store.PlanMessageSource, bool, error)
	LatestHumanProjectMessage(ctx context.Context, projectID domain.ProjectID) (string, time.Time, bool, error)
}

// Check describes the decision a message must back.
type Check struct {
	ProjectID domain.ProjectID
	// Since is when the thing decided on last changed; the message must be newer.
	Since time.Time
	// ID is a chat message id of the owner, or "last".
	ID string
	// CodePrefix names the error codes: "PLAN" gives PLAN_OWNER_MESSAGE_STALE.
	CodePrefix string
	// Verb is what the message does in the "required" error ("approves", "answers").
	Verb string
	// Changed completes "The owner has not written since ..." ("the plan last changed").
	Changed string
	// Older completes "That message is older than ..." ("the plan's last change").
	Older string
	// Hint is appended to the STALE error: another proof that counts.
	Hint string
}

func (c Check) hint() string {
	if c.Hint == "" {
		return ""
	}
	return " (or " + c.Hint + ")"
}

// Verify returns the id of the owner message that backs the decision, or a
// forbidden error saying why none does.
func Verify(ctx context.Context, src Source, c Check) (string, error) {
	code := func(s string) string { return c.CodePrefix + "_OWNER_MESSAGE_" + s }
	id := strings.TrimSpace(c.ID)
	if id == "" {
		return "", apierr.Forbidden(code("REQUIRED"), "An orchestrator needs --owner-message: the owner's chat message that "+c.Verb+"; never "+strings.TrimSuffix(c.Verb, "s")+" on your own")
	}
	if id == "last" {
		mid, at, ok, err := src.LatestHumanProjectMessage(ctx, c.ProjectID)
		if err != nil {
			return "", err
		}
		if !ok || !at.After(c.Since) {
			return "", apierr.Forbidden(code("STALE"), "The owner has not written since "+c.Changed+": ask them first"+c.hint())
		}
		return mid, nil
	}
	m, ok, err := src.BacklogPlanMessageSource(ctx, id)
	if err != nil {
		return "", err
	}
	switch {
	case !ok:
		return "", apierr.Forbidden(code("INVALID"), "That message does not exist")
	case !m.Human || m.ProjectID != c.ProjectID:
		return "", apierr.Forbidden(code("INVALID"), "That message is not the owner's message in this project")
	case !m.At.After(c.Since):
		return "", apierr.Forbidden(code("STALE"), "That message is older than "+c.Older+": ask the owner again"+c.hint())
	}
	return id, nil
}
