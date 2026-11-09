package agent

import "strings"

// credentialScopePrefix marks a model-catalog scope that is not a project but a
// cloud credential kind. A cloud session has no local project, yet its picker
// must show the models the pushed credential can run; encoding the credential
// type into the scope caches each provider's catalog separately with no schema
// change. '@' and ':' cannot appear in a real project ID (projectIDPattern), so
// the project and credential namespaces never collide.
const credentialScopePrefix = "@cred:"

// credentialTypeFromScope returns the credential type a scope carries, if any.
func credentialTypeFromScope(scope string) (string, bool) {
	rest, ok := strings.CutPrefix(scope, credentialScopePrefix)
	if !ok || strings.TrimSpace(rest) == "" {
		return "", false
	}
	return rest, true
}

// modelCatalogScope maps a caller's scope to the cache scope. A credential scope
// has no backing project and caches under its own key; anything else, including
// a project ID, is the single device scope.
func modelCatalogScope(scope string) string {
	if _, ok := credentialTypeFromScope(scope); ok {
		return scope
	}
	return ""
}

// activeModelCatalogScope reports whether a persisted scope is still read: the
// device scope or a credential scope. Older builds also wrote per-project rows.
func activeModelCatalogScope(scope string) bool {
	return modelCatalogScope(scope) == scope
}
