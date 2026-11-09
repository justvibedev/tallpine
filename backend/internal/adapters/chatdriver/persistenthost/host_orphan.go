package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/processalive"
	"tallpine.eu/tallpine/backend/internal/sessionreap"
)

// ProviderRecord is the proof of which process a host launched. A later
// daemon only stops processes this record, or their session env, ties to it.
type ProviderRecord struct {
	PID int `json:"pid"`
	// Start is the provider's OS start time, zero when it could not be read.
	Start time.Time `json:"start,omitzero"`
	// Marker is the session env value the provider was launched with.
	Marker string `json:"marker,omitempty"`
}

// launchRecord captures the host's and provider's identities just after launch.
func launchRecord(providerPID int, env []string) (time.Time, *ProviderRecord) {
	rec := &ProviderRecord{PID: providerPID, Marker: envValue(env, sessionreap.MarkerEnv)}
	procs, err := sessionreap.Snapshot()
	if err != nil {
		return time.Time{}, rec
	}
	var hostStart time.Time
	self := os.Getpid()
	for _, p := range procs {
		switch p.PID {
		case self:
			hostStart = p.Start
		case providerPID:
			rec.Start = p.Start
		}
	}
	return hostStart, rec
}

// envValue returns key's value in env, the last entry winning as it does at exec.
func envValue(env []string, key string) string {
	value := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			value = v
		}
	}
	return value
}

// hostGone reports whether d's host is proven dead. A live pid does not mean a
// live host: when d recorded a start time and the process at that pid started
// at another time, it is an unrelated process that reused the pid. A clock step
// can skew that comparison, and calling a live host dead would kill its agent,
// so a mismatch also needs the host's socket to refuse a connection.
func hostGone(ctx context.Context, d Descriptor, procs []sessionreap.Proc, snapshotOK bool) bool {
	if !processalive.Alive(d.PID) {
		return true
	}
	if !snapshotOK || d.HostStart.IsZero() {
		return false
	}
	for _, p := range procs {
		if p.PID == d.PID && sessionreap.SameStart(p.Start, d.HostStart) {
			return false
		}
	}
	// Not listed at all means a zombie or another user's process, never our host.
	return socketRefused(ctx, d.Address)
}

// socketRefused reports a definite refusal: nothing listens at address, or the
// socket is gone. Any other failure, such as a cancelled context, a timeout or
// running out of descriptors, says nothing about the host.
func socketRefused(ctx context.Context, address string) bool {
	probe, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	conn, err := dialHost(probe, address)
	if err == nil {
		_ = conn.Close()
		return false
	}
	if probe.Err() != nil {
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}

// spareDirs lists where nothing may ever be reaped: the local CI loop's tree,
// which runs on its own schedule and is not a session's to stop.
func spareDirs() ([]string, error) {
	home, err := brand.HomeDir()
	if err != nil {
		return nil, err
	}
	dirs := []string{filepath.Join(home, "local-ci")}
	if custom := brand.Getenv("LOCAL_CI_DIR"); custom != "" {
		dirs = append(dirs, custom)
	}
	return dirs, nil
}

func orphanFor(d Descriptor, spare []string) sessionreap.Orphan {
	return sessionreap.Orphan{PID: d.Provider.PID, Start: d.Provider.Start, Marker: d.Provider.Marker, Spare: spare}
}

// reapOrphans stops the orphans, where the platform can list processes. On
// Windows the host's job object already ended them when the host died.
func reapOrphans(ctx context.Context, orphans []sessionreap.Orphan) error {
	if _, err := sessionreap.New().ReapOrphans(ctx, orphans); err != nil && !errors.Is(err, sessionreap.ErrUnsupported) {
		return err
	}
	return nil
}

// reapDeadHosts claims each dead host's launch lock, stops the providers they
// left behind, and only then clears their descriptors, which are the sole
// record of those providers. Holding the lock keeps a new host from starting
// mid-reap; a host that already replaced the descriptor keeps its files.
func reapDeadHosts(ctx context.Context, dataDir string, dead []Descriptor) error {
	var (
		claimed  []Descriptor
		orphans  []sessionreap.Orphan
		releases []func()
	)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	spare, spareErr := spareDirs()
	for _, d := range dead {
		release, err := claimHostLock(dataDir, d.SessionID, d.PID)
		if err != nil {
			continue
		}
		releases = append(releases, release)
		current, err := readDescriptor(dataDir, d.SessionID)
		if err != nil || current.Token != d.Token {
			continue
		}
		claimed = append(claimed, d)
		if d.Provider != nil {
			orphans = append(orphans, orphanFor(d, spare))
		}
	}
	if len(orphans) > 0 {
		if spareErr != nil {
			return fmt.Errorf("reap orphaned chat agents: %w", spareErr)
		}
		err := reapOrphans(ctx, orphans)
		if errors.Is(err, sessionreap.ErrOwnerUnknown) {
			return nil // Keep the descriptors so the next boot can retry.
		}
		if err != nil {
			return fmt.Errorf("reap orphaned chat agents: %w", err)
		}
	}
	for _, d := range claimed {
		if path, err := descriptorPath(dataDir, d.SessionID); err == nil {
			_ = os.Remove(path)
		}
	}
	return nil
}

// reapPredecessor stops the agent a dead host left, called by the host that
// replaces it while it holds the launch lock. Failure must not block the new
// agent, so it is best effort; the local CI spare check still applies.
func reapPredecessor(ctx context.Context, dataDir, sessionID string) {
	d, err := readDescriptor(dataDir, sessionID)
	if err != nil || d.Provider == nil {
		return
	}
	spare, err := spareDirs()
	if err != nil {
		return
	}
	_ = reapOrphans(ctx, []sessionreap.Orphan{orphanFor(d, spare)})
}
