# Shared harness model catalogs

Settings → Harness → Models, New Task, project role settings and reviewer
pickers use one persisted catalog for each harness and scope. The daemon's
`loadModels` path discovers, refreshes and saves it; `Discoverer.Discover`
combines the harness-specific sources. There is no separate Settings catalog
or `BrowseAll` switch.

The shared catalog includes native command/configuration/ACP/app-server output
and the broader provider sources already supported by each harness:

- Codex: its account-scoped app-server catalog.
- Kiro: `kiro-cli chat --list-models`, once signed in.

Claude models come from the configured provider. Failed discovery retains the
last cached catalog for matching discovery inputs; no hardcoded Claude list is
substituted. Without a matching cache, manual model entry remains available.
A successfully discovered catalog also preserves an explicitly configured model.

Native model caches may include providers without configured credentials.
A **Configured** marker means Tallpine observed credential configuration; it does
not prove that the provider accepts it. Providers with no models are omitted
from Settings. Settings shows the full catalog, including disabled models;
its enable/disable choices feed the shared `useOfferedModels` filter used by
pickers. Already selected models remain visible. Live chat controls retain
the session protocol's supported choices and apply the same filter.

Cached reads return immediately; initial discovery is needed only when that
scope has no saved catalog. Background refreshes and explicit refreshes use
the same pipeline. Database change events notify Settings and all project
pickers that may be reading the global catalog. Older `@browse:all` rows are
ignored during warmup and invalidation.

Project and cloud credential scopes retain their boundaries. Credential-scoped
discovery does not add device-local provider catalogs. Settings omits the
session-history recency lookup that task pickers use, so frontend query keys
can differ while the underlying catalog stays shared. Existing picker display
ordering is preserved.
