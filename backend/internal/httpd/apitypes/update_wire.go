package apitypes

// UpdateResult is the recorded outcome of an update job.
type UpdateResult struct {
	State      string `json:"state" enums:"running,succeeded,failed,rolled_back"`
	Commit     string `json:"commit"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	Message    string `json:"message"`
}

// UpdateStatusResponse compares the installed commit with the newest
// origin/main commit that has a passing local CI row.
type UpdateStatusResponse struct {
	InstalledCommit string        `json:"installedCommit"`
	LatestCommit    string        `json:"latestCommit"`
	LatestCIPassed  bool          `json:"latestCiPassed"`
	Candidate       string        `json:"candidate"`
	UpdateAvailable bool          `json:"updateAvailable"`
	BuildAvailable  bool          `json:"buildAvailable"`
	Running         bool          `json:"running"`
	Last            *UpdateResult `json:"last,omitempty"`
}
