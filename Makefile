# Local verification (GitHub Actions is disabled; this is the merge gate).
# See scripts/verify --help.
.PHONY: verify verify-fast lint lint-changed

verify: ## everything the old CI workflows ran
	scripts/verify --full

verify-fast: ## only what this branch touches vs origin/main, plus repo-scanning Go invariants
	scripts/verify --changed

lint: ## gofmt + golangci-lint + repo-scanning Go invariants
	scripts/verify --lint

lint-changed: ## lint only what this branch changed vs origin/main (workers: use this, not make lint)
	scripts/verify --lint-changed
