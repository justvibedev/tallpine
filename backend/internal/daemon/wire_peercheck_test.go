package daemon

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakePeerRootStore struct {
	review  domain.Review
	session domain.SessionRecord
}

func (f fakePeerRootStore) GetReviewByID(context.Context, string) (domain.Review, bool, error) {
	return f.review, true, nil
}

func (f fakePeerRootStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return f.session, true, nil
}

type fakeRootRuntime struct {
	roots   []int
	err     error
	handles *[]string
}

func (f fakeRootRuntime) ProcessRootPIDs(_ context.Context, handle ports.RuntimeHandle) ([]int, error) {
	*f.handles = append(*f.handles, handle.ID)
	return f.roots, f.err
}

// newTestPeerRoots serves chat host pid 10 under hostID. Pid 10 runs this
// binary unless forged, in which case it runs something else.
func newTestPeerRoots(t *testing.T, store fakePeerRootStore, rt fakeRootRuntime, hostID string, forged bool) peerRoots {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return peerRoots{
		store: store, runtime: rt,
		hostPID:    func(id string) (int, bool) { return 10, id == hostID },
		childrenOf: func(int) ([]int, error) { return nil, nil },
		executableOf: func(int) string {
			if forged {
				return "/tmp/not-tallpine"
			}
			return self
		},
	}
}

func TestChatReviewerRootsComeFromItsChatHost(t *testing.T) {
	var asked []string
	store := fakePeerRootStore{review: domain.Review{InterfaceMode: domain.ReviewerInterfaceChat, ReviewerHandleID: "review-chat:r1"}}
	rt := fakeRootRuntime{err: errors.New("invalid handle"), handles: &asked}
	credential := peeradmit.Credential{ReviewID: "r1"}

	got, err := newTestPeerRoots(t, store, rt, "review-r1", false).AgentRoots(context.Background(), credential)
	if err != nil || !slices.Equal(got, []int{10}) {
		t.Fatalf("roots = %v, %v; want the chat host", got, err)
	}
	if len(asked) != 0 {
		t.Fatalf("runtime was asked about %v, a chat handle is not a runtime handle", asked)
	}
	got, err = newTestPeerRoots(t, store, rt, "review-r1", true).AgentRoots(context.Background(), credential)
	if err != nil || len(got) != 0 {
		t.Fatalf("forged host roots = %v, %v; want none", got, err)
	}
}

func TestTerminalReviewerRootsComeFromTheRuntime(t *testing.T) {
	var asked []string
	store := fakePeerRootStore{review: domain.Review{InterfaceMode: domain.ReviewerInterfaceTUI, ReviewerHandleID: "handle-1"}}
	roots := newTestPeerRoots(t, store, fakeRootRuntime{roots: []int{7}, handles: &asked}, "", false)
	got, err := roots.AgentRoots(context.Background(), peeradmit.Credential{ReviewID: "r1"})
	if err != nil || !slices.Equal(got, []int{7}) || !slices.Equal(asked, []string{"handle-1"}) {
		t.Fatalf("roots = %v, %v, asked %v", got, err, asked)
	}
}

// A real agent whose runtime lookup failed must read as "roots unknown", not as
// an outside process holding its token.
func TestWorkerRootLookupFailureIsRootsUnknown(t *testing.T) {
	var asked []string
	var store fakePeerRootStore
	store.session.Metadata.RuntimeHandleID = "handle-1"
	credential := peeradmit.Credential{Kind: peeradmit.CredentialSession, SessionID: "s1"}
	roots := newTestPeerRoots(t, store, fakeRootRuntime{err: errors.New("tmux probe failed"), handles: &asked}, "", false)

	_, err := roots.AgentRoots(context.Background(), credential)
	if err == nil {
		t.Fatal("runtime failure was dropped")
	}
	set := peeradmit.RootSet{Err: err.Error()}
	peer := peeradmit.Peer{Transport: daemonsock.TransportUnix, Chain: []int{500, 1}, Session: 500}
	if got := peeradmit.Decide(peer, credential, peeradmit.Roots{Agent: set}); got.Reason != peeradmit.ReasonSessionRootsUnknown {
		t.Fatalf("reason = %q, want %q", got.Reason, peeradmit.ReasonSessionRootsUnknown)
	}
}

func TestChatSessionRootsKeepOnlyItsOwnHost(t *testing.T) {
	var asked []string
	var store fakePeerRootStore
	store.session.ID = "s1"
	store.session.Mode = domain.SessionModeChat
	credential := peeradmit.Credential{SessionID: "s1"}
	got, err := newTestPeerRoots(t, store, fakeRootRuntime{handles: &asked}, "s1", false).AgentRoots(context.Background(), credential)
	if err != nil || !slices.Equal(got, []int{10}) || len(asked) != 0 {
		t.Fatalf("roots = %v, %v, asked %v", got, err, asked)
	}
	got, err = newTestPeerRoots(t, store, fakeRootRuntime{handles: &asked}, "s1", true).AgentRoots(context.Background(), credential)
	if err != nil || len(got) != 0 {
		t.Fatalf("forged host roots = %v, %v; want none", got, err)
	}
}

func TestKeepOwnBinaryPIDsDropsForgedDescriptorPID(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executables := map[int]string{10: self, 20: "/tmp/not-tallpine", 30: ""}
	got := keepOwnBinaryPIDs([]int{10, 20, 30}, func(pid int) string { return executables[pid] })
	if !slices.Equal(got, []int{10}) {
		t.Fatalf("kept %v, want only the pid running this binary", got)
	}
}

// handleRuntime answers per handle, for lookups across several terminals.
type handleRuntime map[string]fakeRootRuntime

func (f handleRuntime) ProcessRootPIDs(ctx context.Context, handle ports.RuntimeHandle) ([]int, error) {
	return f[handle.ID].ProcessRootPIDs(ctx, handle)
}

const testDirectHandle = "ptyhost-v1:s1"

func TestDirectHandleRootIsNotAdmittedUnlessItRunsThisBinary(t *testing.T) {
	var asked []string
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var childrenAsked []int
	rt := handleRuntime{
		testDirectHandle: {roots: []int{20}, handles: &asked},
		"tmux-handle":    {roots: []int{30}, handles: &asked},
	}
	foreign := peerRoots{runtime: rt, executableOf: func(int) string { return "/tmp/not-tallpine" },
		childrenOf: func(pid int) ([]int, error) { childrenAsked = append(childrenAsked, pid); return []int{21}, nil }}

	got, err := foreign.ProcessRootPIDs(context.Background(), ports.RuntimeHandle{ID: testDirectHandle})
	if err != nil || len(got) != 0 || len(childrenAsked) != 0 {
		t.Fatalf("forged direct host: roots %v, err %v, children read for %v", got, err, childrenAsked)
	}
	foreign.shellHandles = func(context.Context) ([]string, error) { return []string{testDirectHandle}, nil }
	got, err = foreign.ShellRoots(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("forged direct host as a desktop shell: roots %v, err %v", got, err)
	}
	got, err = foreign.ProcessRootPIDs(context.Background(), ports.RuntimeHandle{ID: "tmux-handle"})
	if err != nil || !slices.Equal(got, []int{30}) {
		t.Fatalf("tmux pane leader runs another program by design: roots %v, err %v", got, err)
	}
	genuine := peerRoots{runtime: rt, executableOf: func(int) string { return self }, childrenOf: func(int) ([]int, error) { return []int{21}, nil }}
	got, err = genuine.ProcessRootPIDs(context.Background(), ports.RuntimeHandle{ID: testDirectHandle})
	if err != nil || !slices.Equal(got, []int{20, 21}) {
		t.Fatalf("real direct host: roots %v, err %v; want host and PTY child", got, err)
	}
}

func TestReviewerAndAgentDirectHandlesGetTheBinaryCheck(t *testing.T) {
	var asked []string
	rt := fakeRootRuntime{roots: []int{20}, handles: &asked}
	review := fakePeerRootStore{review: domain.Review{InterfaceMode: domain.ReviewerInterfaceTUI, ReviewerHandleID: testDirectHandle}}
	got, err := newTestPeerRoots(t, review, rt, "", true).AgentRoots(context.Background(), peeradmit.Credential{ReviewID: "r1"})
	if err != nil || len(got) != 0 {
		t.Fatalf("reviewer with a forged host: roots %v, err %v", got, err)
	}
	var worker fakePeerRootStore
	worker.session.Metadata.RuntimeHandleID = testDirectHandle
	got, err = newTestPeerRoots(t, worker, rt, "", true).AgentRoots(context.Background(), peeradmit.Credential{SessionID: "s1"})
	if err != nil || len(got) != 0 {
		t.Fatalf("worker with a forged host: roots %v, err %v", got, err)
	}
}

func TestOneFailingShellLeavesHealthyShellsAdmitted(t *testing.T) {
	var asked []string
	roots := peerRoots{
		runtime: handleRuntime{
			"stuck":   {err: errors.New("tmux timed out"), handles: &asked},
			"healthy": {roots: []int{40}, handles: &asked},
		},
		shellHandles: func(context.Context) ([]string, error) { return []string{"stuck", "healthy"}, nil },
	}
	pids, err := roots.ShellRoots(context.Background())
	if err == nil || !slices.Equal(pids, []int{40}) {
		t.Fatalf("roots %v, err %v; want the healthy root and the failure", pids, err)
	}
	set := peeradmit.NewRootSet(pids, err, func(pid int) (int, error) { return pid, nil })
	peer := peeradmit.Peer{Transport: daemonsock.TransportUnix, Chain: []int{500, 40}, Session: 500}
	if got := peeradmit.Decide(peer, peeradmit.Credential{Kind: peeradmit.CredentialInstall}, peeradmit.Roots{Shell: set}); got.Class != peeradmit.ClassDesktopShell {
		t.Fatalf("decision = %+v, want the healthy shell admitted", got)
	}
}
