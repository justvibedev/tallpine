package scm

import (
	"context"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (o *Observer) enrichFailureLogs(ctx context.Context, obs *ports.SCMObservation, local domain.PullRequest) {
	if obs.CI.Summary != string(domain.CIFailing) || obs.CI.FailedFingerprint == "" {
		return
	}
	if strings.HasPrefix(local.CIHash, obs.CI.FailedFingerprint+":") {
		checks, err := o.store.ListChecks(ctx, local.URL)
		if err == nil && applyStoredFailedLogTails(obs, checks) {
			return
		}
	}
	tails := make([]string, 0, len(obs.CI.FailedChecks))
	checksByProviderID := make(map[string][]int, len(obs.CI.Checks))
	for i := range obs.CI.Checks {
		key := checkProviderKey(obs.CI.Checks[i])
		checksByProviderID[key] = append(checksByProviderID[key], i)
	}
	for i := range obs.CI.FailedChecks {
		tail := obs.CI.FailedChecks[i].LogTail
		if tail == "" && obs.CI.FailedChecks[i].ProviderID != "" {
			var err error
			repo := ports.SCMRepo{Provider: obs.Provider, Host: obs.Host, Repo: obs.Repo, Owner: ownerOf(obs.Repo), Name: nameOf(obs.Repo)}
			started := time.Now()
			tail, err = o.provider.FetchFailedCheckLogTail(ctx, repo, obs.CI.FailedChecks[i])
			o.logProviderRequest("fetch_failed_check_log", repoFullName(repo), started, err)
			if err != nil {
				tail = "<log fetch failed: " + scrubLine(err.Error()) + ">"
			}
		}
		obs.CI.FailedChecks[i].LogTail = tail
		if tail != "" {
			tails = append(tails, tail)
		}
		for _, j := range checksByProviderID[checkProviderKey(obs.CI.FailedChecks[i])] {
			obs.CI.Checks[j].LogTail = tail
		}
	}
	obs.CI.FailureLogTail = strings.Join(tails, "\n---\n")
}

func checkProviderKey(ch ports.SCMCheckObservation) string {
	return ch.Name + "\x00" + ch.ProviderID
}

func applyStoredFailedLogTails(obs *ports.SCMObservation, checks []domain.PullRequestCheck) bool {
	tailsByName := map[string]string{}
	for _, ch := range checks {
		if obs.CI.HeadSHA != "" && ch.CommitHash != "" && ch.CommitHash != obs.CI.HeadSHA {
			continue
		}
		if ch.LogTail != "" && (ch.Status == domain.PRCheckFailed || ch.Status == domain.PRCheckCancelled) {
			tailsByName[ch.Name] = ch.LogTail
		}
	}
	if len(tailsByName) == 0 {
		return false
	}
	tails := make([]string, 0, len(obs.CI.FailedChecks))
	for i := range obs.CI.FailedChecks {
		tail := tailsByName[obs.CI.FailedChecks[i].Name]
		if tail == "" {
			return false
		}
		obs.CI.FailedChecks[i].LogTail = tail
		tails = append(tails, tail)
	}
	for i := range obs.CI.Checks {
		if tail := tailsByName[obs.CI.Checks[i].Name]; tail != "" {
			obs.CI.Checks[i].LogTail = tail
		}
	}
	obs.CI.FailureLogTail = strings.Join(tails, "\n---\n")
	return true
}
