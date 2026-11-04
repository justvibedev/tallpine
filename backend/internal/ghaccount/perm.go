package ghaccount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// PushChecker reports whether a token can push to a repo.
type PushChecker interface {
	CanPush(ctx context.Context, token string, repo Repo) (bool, error)
}

// RESTPushChecker asks GET /repos/{owner}/{repo} for permissions.push.
type RESTPushChecker struct {
	HTTP    *http.Client
	BaseURL string // default https://api.github.com
}

// CanPush is false for 404/403 (no access), an error for anything unexpected so
// a transient failure is not cached as "cannot push".
func (c RESTPushChecker) CanPush(ctx context.Context, token string, repo Repo) (bool, error) {
	base := c.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	u := base + "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("github repo permission check: %w", redactErr(err))
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusForbidden, http.StatusUnauthorized:
		return false, nil
	default:
		return false, fmt.Errorf("github repo permission check: status %d", resp.StatusCode)
	}
	var body struct {
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return false, fmt.Errorf("github repo permission check: decode: %w", err)
	}
	return body.Permissions.Push, nil
}

// redactErr drops the URL wrapper so nothing request-derived leaks into logs.
func redactErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// IdentityChecker reports which login a token authenticates as.
type IdentityChecker interface {
	Login(ctx context.Context, token string) (string, error)
}

// RESTIdentityChecker asks GET /user (the same answer as `gh api user`).
type RESTIdentityChecker struct {
	HTTP    *http.Client
	BaseURL string // default https://api.github.com
}

// Login returns the token's login.
func (c RESTIdentityChecker) Login(ctx context.Context, token string) (string, error) {
	base := c.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/user", http.NoBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github identity check: %w", redactErr(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github identity check: status %d", resp.StatusCode)
	}
	var body struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("github identity check: decode: %w", err)
	}
	return body.Login, nil
}
