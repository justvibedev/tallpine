package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/runtime/runtimeselect"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/buildinfo"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/procmem"
)

// startPeerCheck builds the daemon peer check (B631): enforcing on a signed
// macOS build unless the owner's switch is off, inactive otherwise. The
// switch is applied before the server serves and owned by the settings writer.
func (b *boot) startPeerCheck() *peeradmit.Checker {
	mode, reason := peeradmit.DefaultMode(runtime.GOOS, buildinfo.TeamID, peeradmit.SignatureCheckBuilt)
	checker := peeradmit.New(peeradmit.Options{
		Mode: mode, ModeReason: reason, TeamID: buildinfo.TeamID,
		Verifier: peeradmit.NewDesktopVerifier(buildinfo.TeamID, brand.BundleID),
		Roots: peerRoots{
			store: b.store, runtime: b.runtimeAdapter, hostPID: b.chatHostPID, executableOf: procmem.ExecutablePath, childrenOf: procmem.ChildPIDs,
			shellHandles: func(ctx context.Context) ([]string, error) {
				if b.shellTermSvc == nil {
					return nil, nil
				}
				return b.shellTermSvc.LiveHandleIDs(ctx)
			},
		},
		Log: b.log,
	})
	applyStoredPeerCheck(b.ctx, checker, func(ctx context.Context) (string, error) {
		row, err := b.store.GetAppSettings(ctx)
		return row.PeerCheck, err
	}, b.log)
	if b.settingsWriter != nil {
		b.settingsWriter.PeerCheck = peerCheckSwitch{checker: checker, store: b.store, now: time.Now}
	}
	go checker.Run(b.ctx)
	return checker
}

// peerRootStore is what peerRoots reads about a token's principal.
type peerRootStore interface {
	GetReviewByID(ctx context.Context, id string) (domain.Review, bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// peerRoots reads a token principal's runtime roots live from the runtime.
type peerRoots struct {
	store        peerRootStore
	runtime      ports.RuntimeProcessRootInspector
	hostPID      func(hostID string) (int, bool)
	executableOf func(pid int) string
	childrenOf   func(pid int) ([]int, error)
	shellHandles func(ctx context.Context) ([]string, error)
}

// AgentRoots returns the roots of the session or review a token belongs to. An
// error means the roots are unknown, which is not the same as having none.
func (r peerRoots) AgentRoots(ctx context.Context, credential peeradmit.Credential) ([]int, error) {
	if credential.ReviewID != "" {
		return r.reviewRoots(ctx, credential.ReviewID)
	}
	rec, found, err := r.store.GetSession(ctx, domain.SessionID(credential.SessionID))
	if err != nil || !found {
		return nil, err
	}
	roots, err := sessionRootPIDs(ctx, r, r.hostPID, rec)
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		roots = keepOwnBinaryPIDs(roots, r.executableOf)
	}
	return roots, err
}

// ProcessRootPIDs reads a runtime handle's roots for the peer check. A direct
// handle's host pid can come from a registry file the same user can write, so
// it must be running this daemon's binary. The agent is the host's PTY child,
// which leads a POSIX session of its own, so it is a root too: without it a
// job orphaned inside that session would not be under any root.
func (r peerRoots) ProcessRootPIDs(ctx context.Context, handle ports.RuntimeHandle) ([]int, error) {
	pids, err := r.runtime.ProcessRootPIDs(ctx, handle)
	if err != nil || !runtimeselect.IsDirectHandle(handle.ID) {
		return pids, err
	}
	hosts := keepOwnBinaryPIDs(pids, r.executableOf)
	roots := slices.Clone(hosts)
	var failures []error
	for _, host := range hosts {
		children, err := r.childrenOf(host)
		if err != nil && !errors.Is(err, procmem.ErrProcessGone) {
			failures = append(failures, err)
			continue
		}
		roots = append(roots, children...)
	}
	return roots, errors.Join(failures...)
}

// reviewRoots reads a reviewer's roots. A chat reviewer has a chat host; its
// stored handle is a review-chat label the runtime cannot look up.
func (r peerRoots) reviewRoots(ctx context.Context, reviewID string) ([]int, error) {
	review, found, err := r.store.GetReviewByID(ctx, reviewID)
	if err != nil || !found {
		return nil, err
	}
	if review.InterfaceMode == domain.ReviewerInterfaceChat {
		pid, ok := r.hostPID(string(domain.ReviewChatHostID(reviewID)))
		if !ok {
			return nil, nil
		}
		return keepOwnBinaryPIDs([]int{pid}, r.executableOf), nil
	}
	if review.ReviewerHandleID == "" {
		return nil, nil
	}
	return r.ProcessRootPIDs(ctx, ports.RuntimeHandle{ID: review.ReviewerHandleID})
}

// ShellRoots returns the roots of every terminal opened in the desktop app. A
// failed lookup is returned with the roots that were read, so one stuck
// terminal does not make every other shell unknown.
func (r peerRoots) ShellRoots(ctx context.Context) ([]int, error) {
	handles, err := r.shellHandles(ctx)
	if err != nil {
		return nil, err
	}
	var roots []int
	var failures []error
	for _, id := range handles {
		pids, err := r.ProcessRootPIDs(ctx, ports.RuntimeHandle{ID: id})
		if err != nil {
			failures = append(failures, err)
		}
		roots = append(roots, pids...)
	}
	return roots, errors.Join(failures...)
}

// keepOwnBinaryPIDs drops pids not running this daemon's executable. A chat
// host pid comes from a descriptor file the same user can write, so the file
// alone must not be able to name another program as a session root.
func keepOwnBinaryPIDs(pids []int, executableOf func(int) string) []int {
	self, err := os.Executable()
	if err != nil {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		resolved = self
	}
	kept := pids[:0:0]
	for _, pid := range pids {
		if exe := executableOf(pid); exe != "" && (exe == self || exe == resolved) {
			kept = append(kept, pid)
		}
	}
	return kept
}
