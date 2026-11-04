package ports

import (
	"context"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// AuthEvidence collects what an authentication check relied on, so readiness
// can say why a harness reads as signed in. Adapters record through
// RecordAuthEvidence; without a collector in the context it does nothing.
type AuthEvidence struct {
	mu          sync.Mutex
	identity    domain.AgentAccountIdentity
	diagnostics []domain.AgentAuthDiagnostic
}

type authEvidenceKey struct{}

// WithAuthEvidence returns a context that collects authentication evidence.
func WithAuthEvidence(ctx context.Context) (context.Context, *AuthEvidence) {
	evidence := &AuthEvidence{}
	return context.WithValue(ctx, authEvidenceKey{}, evidence), evidence
}

// RecordAuthEvidence records what made a check succeed. A later record
// replaces an earlier one, so adapters record at the point they decide.
func RecordAuthEvidence(ctx context.Context, identity domain.AgentAccountIdentity) {
	evidence, ok := ctx.Value(authEvidenceKey{}).(*AuthEvidence)
	if !ok || evidence == nil {
		return
	}
	evidence.mu.Lock()
	evidence.identity = identity
	evidence.mu.Unlock()
}

// AddAuthEvidence fills fields the recorded evidence leaves empty, such as
// an email learned after the deciding file or variable was recorded.
func AddAuthEvidence(ctx context.Context, identity domain.AgentAccountIdentity) {
	evidence, ok := ctx.Value(authEvidenceKey{}).(*AuthEvidence)
	if !ok || evidence == nil {
		return
	}
	evidence.mu.Lock()
	evidence.identity = evidence.identity.Merge(identity)
	evidence.mu.Unlock()
}

// Identity returns the recorded evidence.
func (e *AuthEvidence) Identity() domain.AgentAccountIdentity {
	if e == nil {
		return domain.AgentAccountIdentity{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.identity
}

// RecordAuthDiagnostic retains an adapter-authored check outcome, including
// unsuccessful attempts. Never pass raw errors, credential values or CLI output.
func RecordAuthDiagnostic(ctx context.Context, diagnostic domain.AgentAuthDiagnostic) {
	evidence, ok := ctx.Value(authEvidenceKey{}).(*AuthEvidence)
	if !ok || evidence == nil {
		return
	}
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	if len(evidence.diagnostics) < 64 {
		evidence.diagnostics = append(evidence.diagnostics, diagnostic)
	}
}

// Diagnostics returns an independent snapshot of the recorded check outcomes.
func (e *AuthEvidence) Diagnostics() []domain.AgentAuthDiagnostic {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]domain.AgentAuthDiagnostic(nil), e.diagnostics...)
}

// AgentAuthDiagnosticsChecker adds the signed-in account and evidence from the
// same check as the verdict, so neither needs a second probe.
type AgentAuthDiagnosticsChecker interface {
	AuthStatusWithDiagnostics(context.Context) (AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error)
}
