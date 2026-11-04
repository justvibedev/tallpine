package accountadopt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/adapters/agentaccount"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/accountadopt"
	"tallpine.eu/tallpine/backend/internal/service/agentaccounts"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// signedInClaude is the real folder linking of the Claude adapter with a login
// that needs no CLI: the test never runs claude.
type signedInClaude struct{ *agentaccount.Claude }

func (signedInClaude) Identity(context.Context, string) (domain.AgentAccountIdentityCheck, error) {
	return domain.AgentAccountIdentityCheck{SignedIn: true, LoggedIn: true, Email: "b@example.com"}, nil
}

type noSessions struct{}

func (noSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) { return nil, nil }
func (noSessions) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{}, false, nil
}

type noMover struct{}

func (noMover) StartAccountTransition(context.Context, domain.SessionID, string, domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error) {
	return domain.SessionInterfaceTransition{}, nil
}
func (noMover) GetSessionInterfaceTransition(context.Context, string) (domain.SessionInterfaceTransition, bool, error) {
	return domain.SessionInterfaceTransition{}, false, nil
}

// A target folder that keeps its own history is refused up front with the
// split-history 409, through the real folder check on temp dirs: the default
// stays where it was and no batch is stored.
func TestSplitHistoryTargetIsRefusedWithA409AndChangesNothing(t *testing.T) {
	root := t.TempDir()
	device := filepath.Join(root, "device")
	home := filepath.Join(root, "accounts", "claude", "acct-b")
	for _, dir := range []string{filepath.Join(device, "projects", "p"), filepath.Join(home, "projects", "own")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "projects", "own", "x.jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateAgentAccount(ctx, domain.AgentAccount{ID: "acct-b", Provider: domain.AgentAccountProviderClaude, Home: home, Email: "b@example.com"}); err != nil {
		t.Fatal(err)
	}
	claude := signedInClaude{&agentaccount.Claude{DeviceHome: device, Binary: func(context.Context) (string, error) { return "", os.ErrNotExist }}}
	accounts := agentaccounts.New(store, []ports.AgentAccountProviderCLI{claude}, filepath.Join(root, "accounts"), nil, nil)
	svc := accountadopt.New(accountadopt.Config{Store: store, Accounts: accounts, Sessions: noSessions{}, Mover: noMover{}})

	router := chi.NewRouter()
	(&controllers.AccountAdoptController{Svc: svc}).Register(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/agents/accounts/acct-b/adopt", strings.NewReader(`{"policy":"wait","requestKey":"k"}`)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "AGENT_ACCOUNT_SPLIT_HISTORY") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if def, ok, _ := store.GetAgentAccountDefault(ctx, domain.AgentAccountProviderClaude); ok {
		t.Fatalf("a refused switch set the default to %q", def)
	}
	if _, ok, _ := store.GetLatestAccountAdoption(ctx, "acct-b"); ok {
		t.Fatal("a refused switch stored a batch")
	}
	if rec := httptest.NewRecorder(); true {
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agents/accounts/acct-b/adopt/preview", nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("preview %d %s", rec.Code, rec.Body.String())
		}
	}
}

func (noMover) GetActiveSessionInterfaceTransition(context.Context, domain.SessionID) (domain.SessionInterfaceTransition, bool, error) {
	return domain.SessionInterfaceTransition{}, false, nil
}
