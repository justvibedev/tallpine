package controllers

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

// view is the only SessionView constructor: it applies the persisted read
// model and the live runtime facts (pause, waiting, background tasks).
func (c *SessionsController) view(ctx context.Context, r *http.Request, s domain.Session) SessionView {
	v := baseSessionView(r, s)
	if v.IsTerminated {
		return v
	}
	facts := sessionsvc.NewRuntimeFacts(c.UsageLimits, c.BackgroundTasks)
	if until, ok := facts.PausedUntil(ctx, s.ID); ok {
		u := until.UTC()
		v.PausedUntil = &u
	}
	v.ReauthRequired = facts.ReauthRequired(ctx, s.ID)
	v.WaitingOn = facts.Waiting(s.ID)
	v.BackgroundTasks = backgroundTaskPayloads(facts.Running(s.ID))
	return v
}

func (c *SessionsController) views(ctx context.Context, r *http.Request, sessions []domain.Session) []SessionView {
	out := make([]SessionView, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, c.view(ctx, r, s))
	}
	return out
}

func baseSessionView(r *http.Request, s domain.Session) SessionView {
	terminalGeneration := s.Metadata.RuntimeLaunchID
	view := SessionView{
		Session:            s,
		Branch:             s.Metadata.Branch,
		TerminalGeneration: terminalGeneration,
		PreviewURL:         s.Metadata.PreviewURL,
		PreviewRevision:    s.Metadata.PreviewRevision,
		Model:              s.Metadata.Model,
		LastUserMessageAt: func() *time.Time {
			if s.Metadata.LatestUserPromptAt.IsZero() {
				return nil
			}
			at := s.Metadata.LatestUserPromptAt
			return &at
		}(),
		LastInteractionAt: func() *time.Time {
			at := s.Metadata.LatestUserPromptAt
			if s.Metadata.LatestInteractionAt.After(at) {
				at = s.Metadata.LatestInteractionAt
			}
			if at.IsZero() {
				return nil
			}
			return &at
		}(),
		LastEventAt:   s.LastEventAt(),
		PRs:           sessionPRFacts(s.PRs),
		HasArtifacts:  s.OutputType.HasArtifact() || len(s.ArtifactFiles) > 0,
		ArtifactFiles: sessionArtifactFiles(r, s),
	}
	return view
}

func sessionArtifactFiles(r *http.Request, s domain.Session) []SessionArtifactView {
	out := make([]SessionArtifactView, 0, len(s.ArtifactFiles))
	for _, artifact := range s.ArtifactFiles {
		view := SessionArtifactView{
			Path:      artifact.Path,
			Name:      artifact.Name,
			Kind:      artifact.Kind,
			Size:      artifact.Size,
			UpdatedAt: artifact.UpdatedAt,
		}
		// A distinct host (not a shared path-prefix marker) gives every
		// artifact link an unambiguous source identity: it can never collide
		// with a real workspace file, unlike the legacy __ao_artifacts__/
		// path-prefix form previewFile/previewOriginEntry still accept for
		// backward compatibility.
		if rawURL, ok := artifactRawURL(r, s.ID, artifact.Path); ok {
			view.RawURL = rawURL
		}
		if artifact.Kind == domain.SessionArtifactHTML {
			view.PreviewURL, _ = previewutil.ArtifactFileURL("http://"+r.Host, s.ID, artifact.Path)
			view.InlineURL, _ = previewutil.InlineArtifactFileURL("http://"+r.Host, s.ID, artifact.Path)
		}
		out = append(out, view)
	}
	return out
}

// artifactRawURL builds the raw-bytes fetch URL for one artifact file on the
// artifact preview origin, with ?raw=true so a markdown artifact returns its
// source text rather than server-rendered HTML (matching the ?raw=true
// behavior serveOpenedPreviewFile already applies on every preview route).
func artifactRawURL(r *http.Request, id domain.SessionID, filePath string) (string, bool) {
	raw, err := previewutil.ArtifactFileURL("http://"+r.Host, id, filePath)
	if err != nil {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	q := u.Query()
	q.Set("raw", "true")
	u.RawQuery = q.Encode()
	return u.String(), true
}

func sessionPRFacts(prs []domain.PRFacts) []SessionPRFacts {
	out := make([]SessionPRFacts, 0, len(prs))
	for _, pr := range prs {
		out = append(out, SessionPRFacts{URL: pr.URL, Number: pr.Number, State: string(pr.State()), CI: pr.CI, Review: pr.Review, Mergeability: pr.Mergeability, ReviewComments: pr.ReviewComments, UpdatedAt: pr.UpdatedAt})
	}
	return out
}

func sessionPRSummaries(prs []sessionsvc.PRSummary) []SessionPRSummary {
	out := make([]SessionPRSummary, 0, len(prs))
	for _, pr := range prs {
		out = append(out, NewSessionPRSummary(pr))
	}
	return out
}

func nonNilSessionIDs(ids []domain.SessionID) []domain.SessionID {
	if ids == nil {
		return []domain.SessionID{}
	}
	return ids
}

func backgroundTaskPayloads(tasks []bgtasks.Info) []BackgroundTaskPayload {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]BackgroundTaskPayload, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, BackgroundTaskPayload{ID: task.ID, Name: task.Name})
	}
	return out
}
