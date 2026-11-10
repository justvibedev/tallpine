//go:build darwin || linux

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/runtime/conpty"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/procmem"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

// directPrefixRuntime strips the handle prefix the way the hybrid runtime does.
type directPrefixRuntime struct{ rt *conpty.Runtime }

func (d directPrefixRuntime) ProcessRootPIDs(ctx context.Context, handle ports.RuntimeHandle) ([]int, error) {
	return d.rt.ProcessRootPIDs(ctx, ports.RuntimeHandle{ID: strings.TrimPrefix(handle.ID, "ptyhost-v1:")})
}

// A background job whose parent has exited has no host in its chain. It must
// still be admitted through the POSIX session the PTY child leads.
func TestDirectRuntimeJobOutlivingItsParentIsAdmitted(t *testing.T) {
	root, err := os.MkdirTemp(testenv.ShortBaseDir(), "pc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("HOME", root)
	jobFile := filepath.Join(root, "job")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	rt := conpty.New(conpty.Options{RunFilePath: filepath.Join(root, "running.json")})
	script := "( /bin/sleep 120 & echo $! > " + jobFile + ".tmp; mv " + jobFile + ".tmp " + jobFile + " ); exec /bin/sleep 120"
	handle, err := rt.Create(ctx, ports.RuntimeConfig{SessionID: domain.SessionID("peer-direct"), WorkspacePath: root, Argv: []string{"/bin/sh", "-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Destroy(context.Background(), handle) })

	job := waitForPIDFile(t, jobFile)
	t.Cleanup(func() { _ = syscall.Kill(job, syscall.SIGKILL) })

	roots := peerRoots{runtime: directPrefixRuntime{rt}, executableOf: procmem.ExecutablePath, childrenOf: procmem.ChildPIDs}
	directHandle := ports.RuntimeHandle{ID: "ptyhost-v1:" + handle.ID}
	var pids []int
	var chain []int
	deadline := time.Now().Add(15 * time.Second)
	for {
		pids, err = roots.ProcessRootPIDs(ctx, directHandle)
		if err != nil {
			t.Fatal(err)
		}
		chain, err = procmem.ParentChain(job)
		if err != nil {
			t.Fatal(err)
		}
		if len(pids) >= 2 && !slices.Contains(chain, pids[0]) && !slices.Contains(chain, pids[1]) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job never orphaned: roots %v, chain %v", pids, chain)
		}
		time.Sleep(50 * time.Millisecond)
	}

	session, err := procmem.SessionOf(job)
	if err != nil {
		t.Fatal(err)
	}
	set := peeradmit.NewRootSet(pids, nil, procmem.SessionOf)
	peer := peeradmit.Peer{Transport: daemonsock.TransportUnix, Chain: chain, Session: session}
	credential := peeradmit.Credential{Kind: peeradmit.CredentialSession, SessionID: "peer-direct"}
	got := peeradmit.Decide(peer, credential, peeradmit.Roots{Agent: set})
	if got.Class != peeradmit.ClassAgent {
		t.Fatalf("decision = %+v; roots %v, sessions %v, job session %d, chain %v", got, pids, set.Sessions, session, chain)
	}
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if convErr != nil {
				t.Fatalf("job file %q: %v", raw, convErr)
			}
			return pid
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("job pid never written")
	return 0
}
