package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

var trailerLinePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*: \S`)

// trailerBlock returns the last paragraph of body whose lines are all git
// trailers ("Key: value"), skipping a trailing "Generated with" footer
// paragraph. It returns "" when there is none.
func trailerBlock(body string) string {
	paras := strings.Split(strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n"), "\n\n")
	if n := len(paras); n > 1 && strings.Contains(paras[n-1], "Generated with") {
		paras = paras[:n-1]
	}
	last := strings.TrimSpace(paras[len(paras)-1])
	if last == "" {
		return ""
	}
	for _, line := range strings.Split(last, "\n") {
		if !trailerLinePattern.MatchString(line) {
			return ""
		}
	}
	return last
}

// squashMessage returns the commit title and message for a squash merge of
// pr when its body carries trailers, mirroring the repository's
// COMMIT_MESSAGES default (commit bodies plus Co-authored-by lines) with the
// body trailers appended last. It returns empty strings to keep GitHub's
// default, which happens when there are no trailers or a read fails.
func (p *Provider) squashMessage(ctx context.Context, pr ports.SCMPRRef) (title, message string) {
	client := p.clientFor(pr.Repo.Host)
	base := repoPath(pr.Repo.Owner, pr.Repo.Name, "pulls", strconv.Itoa(pr.Number))
	resp, err := client.doREST(ctx, http.MethodGet, base, nil, nil)
	var pull struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err == nil {
		err = json.Unmarshal(resp.Body, &pull)
	}
	if err != nil {
		p.logger.Warn("github scm: squash trailers not applied: read pull", "pr", pr.Number, "error", err)
		return "", ""
	}
	trailers := trailerBlock(pull.Body)
	if trailers == "" || strings.TrimSpace(pull.Title) == "" {
		return "", ""
	}
	q := url.Values{"per_page": {"100"}}
	resp, err = client.doREST(ctx, http.MethodGet, base+"/commits", q, nil)
	var commits []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"author"`
		} `json:"commit"`
	}
	if err == nil {
		err = json.Unmarshal(resp.Body, &commits)
	}
	if err != nil {
		p.logger.Warn("github scm: squash trailers not applied: read commits", "pr", pr.Number, "error", err)
		return "", ""
	}

	var parts, coAuthors []string
	seenAuthor := map[string]bool{}
	for _, c := range commits {
		msg := strings.TrimSpace(c.Commit.Message)
		if len(commits) == 1 {
			_, msg, _ = strings.Cut(msg, "\n")
			msg = strings.TrimSpace(msg)
		} else {
			msg = "* " + msg
		}
		if msg != "" {
			parts = append(parts, msg)
		}
		a := c.Commit.Author
		if key := strings.ToLower(a.Email); a.Email != "" && !seenAuthor[key] && !strings.EqualFold(c.Author.Login, pull.User.Login) {
			coAuthors = append(coAuthors, fmt.Sprintf("Co-authored-by: %s <%s>", a.Name, a.Email))
		}
		seenAuthor[strings.ToLower(a.Email)] = true
	}
	return strings.TrimSpace(pull.Title) + fmt.Sprintf(" (#%d)", pr.Number),
		joinSquashMessage(strings.Join(parts, "\n\n"), append(coAuthors, strings.Split(trailers, "\n")...))
}

// joinSquashMessage appends trailer lines to base as the final paragraph,
// removing any case-insensitive duplicates of them from base first.
func joinSquashMessage(base string, trailers []string) string {
	want := map[string]bool{}
	var block []string
	for _, t := range trailers {
		if k := strings.ToLower(strings.TrimSpace(t)); !want[k] {
			want[k] = true
			block = append(block, strings.TrimSpace(t))
		}
	}
	var kept []string
	for _, line := range strings.Split(base, "\n") {
		if !want[strings.ToLower(strings.TrimSpace(line))] {
			kept = append(kept, line)
		}
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	if out != "" {
		out += "\n\n"
	}
	return out + strings.Join(block, "\n")
}
