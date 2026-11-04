package update

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

// fakeReleaseLocal records its arguments and obeys FAKE_* switches:
//
//	build:    creates the app bundle; FAKE_BUILD_FAIL=1 then exits 1
//	install:  fails when FAKE_INSTALL_FAIL=1; otherwise leaves the daemon
//	          ready (FAKE_READY_AFTER=1) on commit FAKE_COMMIT
//	rollback: leaves the daemon ready on the old commit
const fakeReleaseLocal = `#!/bin/bash
echo "$*" >>"$T/calls"
case "$1" in
build)
  mkdir -p "$UPDATE_STATE_DIR/release-builds/$UPDATE_SHA/frontend/out/x/Tallpine.app"
  if [[ "${FAKE_BUILD_FAIL:-0}" == 1 ]]; then exit 1; fi ;; # a failed build can leave a partial app
install)
  if [[ "${FAKE_INSTALL_FAIL:-0}" == 1 ]]; then
    if [[ "${FAKE_FAIL_READY:-0}" == 1 ]]; then touch "$T/ready"; else rm -f "$T/ready"; fi
    exit 7
  fi
  if [[ "${FAKE_READY_AFTER:-1}" == 1 ]]; then touch "$T/ready"; else rm -f "$T/ready"; fi
  echo -n "${FAKE_COMMIT:-$UPDATE_SHA}" >"$T/commit"
  if [[ "${FAKE_EMPTY_COMMIT:-0}" == 1 ]]; then echo -n >"$T/commit"; fi ;;
rollback)
  # "restore by hand": the undo can leave the live bundle (and build) gone.
  if [[ "${FAKE_ROLLBACK_DELETES_LIVE:-0}" == 1 ]]; then rm -rf "$UPDATE_APP_PATH"; fi
  if [[ "${FAKE_ROLLBACK_DELETES_BUILD:-0}" == 1 ]]; then rm -rf "$UPDATE_STATE_DIR/release-builds/$UPDATE_SHA/frontend"; fi
  if [[ "${FAKE_ROLLBACK_UNHEALTHY:-0}" == 1 ]]; then rm -f "$T/ready"; else touch "$T/ready"; echo -n abcdef0123 >"$T/commit"; fi ;;
esac
`

type jobEnv struct {
	t     *testing.T
	state string
	tmp   string
	env   []string
	// handler is the fake daemon, also servable on a socket.
	handler http.Handler
}

func newJobEnv(t *testing.T) *jobEnv {
	t.Helper()
	tmp := t.TempDir()
	state := filepath.Join(tmp, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(tmp, "release-local")
	if err := os.WriteFile(fake, []byte(fakeReleaseLocal), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(tmp, "commit"), []byte("abcdef0123"), 0o600)
	os.WriteFile(filepath.Join(tmp, "ready"), nil, 0o600) // the old daemon is up
	live := filepath.Join(tmp, "Live.app")
	os.MkdirAll(live, 0o700)
	os.WriteFile(filepath.Join(live, "marker"), []byte("live"), 0o600)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			if _, err := os.Stat(filepath.Join(tmp, "ready")); err != nil {
				http.Error(w, "no", http.StatusServiceUnavailable)
			}
		case "/healthz":
			c, _ := os.ReadFile(filepath.Join(tmp, "commit"))
			w.Write([]byte(`{"build":{"commit":"` + string(c) + `"}}`))
		}
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	// A harmless open command that records the launch.
	opener := filepath.Join(tmp, "open")
	os.WriteFile(opener, []byte("#!/bin/sh\necho \"$@\" >>\""+tmp+"/opened\"\n"), 0o700)
	// A real, portable stand-in for ditto (macOS only): copies the bundle tree.
	copier := filepath.Join(tmp, "copy")
	os.WriteFile(copier, []byte("#!/bin/sh\nexec cp -R \"$1\" \"$2\"\n"), 0o700)
	return &jobEnv{t: t, state: state, tmp: tmp, handler: handler, env: append(os.Environ(),
		"T="+tmp, "UPDATE_SHA="+sha, "UPDATE_STATE_DIR="+state, "UPDATE_RELEASE_LOCAL="+fake,
		"UPDATE_PORT="+u.Port(), "UPDATE_DELAY=0", "UPDATE_POLL=1", "UPDATE_HEALTH_WAIT=2",
		"UPDATE_OPEN_CMD="+opener, "UPDATE_APP_PATH="+live,
		"UPDATE_APP_PGREP=vo-test-no-such-app-process", "UPDATE_VERIFY_CMD=true",
		"UPDATE_COPY_CMD="+copier,
	)}
}

func (j *jobEnv) run(extra ...string) (int, Result) {
	j.t.Helper()
	cmd := exec.Command("/bin/bash", filepath.Join("job.sh"))
	cmd.Env = append(j.env, extra...)
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		j.t.Fatalf("run: %v\n%s", err, out)
	}
	var r Result
	if data, err := os.ReadFile(filepath.Join(j.state, "update", "result.json")); err == nil {
		_ = json.Unmarshal(data, &r)
	}
	return code, r
}

func (j *jobEnv) calls() string {
	b, _ := os.ReadFile(filepath.Join(j.tmp, "calls"))
	return string(b)
}

func (j *jobEnv) opened() bool {
	_, err := os.Stat(filepath.Join(j.tmp, "opened"))
	return err == nil
}

func TestJobSuccessBuildsInstallsAndVerifies(t *testing.T) {
	j := newJobEnv(t)
	code, r := j.run()
	if code != 0 || r.State != "succeeded" || r.Commit != sha {
		t.Fatalf("code %d result %+v", code, r)
	}
	c := j.calls()
	if !strings.Contains(c, "build "+sha) || !strings.Contains(c, "install --yes --allow-in-session ") || strings.Contains(c, "rollback") {
		t.Fatalf("calls:\n%s", c)
	}
	if _, err := os.Stat(filepath.Join(j.state, "update", "lock")); err == nil {
		t.Fatal("lock not released")
	}
}

func TestJobSkipsBuildWhenAlreadyBuilt(t *testing.T) {
	j := newJobEnv(t)
	os.MkdirAll(filepath.Join(j.state, "release-builds", sha, "frontend", "out", "x", "Tallpine.app"), 0o700)
	if code, r := j.run(); code != 0 || r.State != "succeeded" {
		t.Fatalf("code %d result %+v", code, r)
	}
	if strings.Contains(j.calls(), "build "+sha) {
		t.Fatalf("rebuilt:\n%s", j.calls())
	}
}

func TestJobBuildFailureLeavesInstalledAppAlone(t *testing.T) {
	j := newJobEnv(t)
	code, r := j.run("FAKE_BUILD_FAIL=1")
	if code == 0 || r.State != "failed" || strings.Contains(j.calls(), "install") {
		t.Fatalf("code %d result %+v calls %s", code, r, j.calls())
	}
}

func TestJobInstallFailureRollsBackWhenDaemonDown(t *testing.T) {
	j := newJobEnv(t)
	code, r := j.run("FAKE_INSTALL_FAIL=1")
	if code == 0 || r.State != "rolled_back" || !strings.Contains(j.calls(), "rollback --yes --allow-in-session") {
		t.Fatalf("code %d result %+v calls %s", code, r, j.calls())
	}
}

func TestJobInstallFailureWithHealthyDaemonDoesNotRollBack(t *testing.T) {
	j := newJobEnv(t)
	_, r := j.run("FAKE_INSTALL_FAIL=1", "FAKE_FAIL_READY=1")
	if r.State != "failed" || strings.Contains(j.calls(), "rollback") {
		t.Fatalf("result %+v calls %s", r, j.calls())
	}
}

func TestJobUnhealthyNewVersionRollsBack(t *testing.T) {
	j := newJobEnv(t)
	_, r := j.run("FAKE_READY_AFTER=0")
	if r.State != "rolled_back" || !strings.Contains(j.calls(), "rollback") {
		t.Fatalf("result %+v calls %s", r, j.calls())
	}
}

func TestJobWrongCommitRollsBack(t *testing.T) {
	j := newJobEnv(t)
	_, r := j.run("FAKE_COMMIT=ffffffffffffffffffffffffffffffffffffffff")
	if r.State != "rolled_back" {
		t.Fatalf("result %+v calls %s", r, j.calls())
	}
}

func TestJobRelaunchesAppWhenNotRunning(t *testing.T) {
	j := newJobEnv(t)
	if code, r := j.run(); code != 0 || r.State != "succeeded" {
		t.Fatalf("code %d result %+v", code, r)
	}
	if !j.opened() {
		t.Fatal("app was not relaunched")
	}
}

func TestJobLeavesRunningAppAlone(t *testing.T) {
	j := newJobEnv(t)
	sleeper := exec.Command("sleep", "30.4242")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() })
	if code, r := j.run("UPDATE_APP_PGREP=sleep 30.4242"); code != 0 || r.State != "succeeded" {
		t.Fatalf("code %d result %+v", code, r)
	}
	if j.opened() {
		t.Fatal("relaunched a running app")
	}
}

func TestJobRefusesConcurrentStart(t *testing.T) {
	j := newJobEnv(t)
	holder := exec.Command("sleep", "30")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	lock := filepath.Join(j.state, "update", "lock")
	os.MkdirAll(lock, 0o700)
	os.WriteFile(filepath.Join(lock, "pid"), []byte(strconv.Itoa(holder.Process.Pid)), 0o600)
	code, r := j.run()
	if code != 3 || r.State != "" || j.calls() != "" {
		t.Fatalf("code %d result %+v calls %s", code, r, j.calls())
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("refused job removed the holder's lock")
	}
}

func TestJobTakesOverStaleLock(t *testing.T) {
	j := newJobEnv(t)
	lock := filepath.Join(j.state, "update", "lock")
	os.MkdirAll(lock, 0o700)
	os.WriteFile(filepath.Join(lock, "pid"), []byte("999999"), 0o600)
	if code, r := j.run(); code != 0 || r.State != "succeeded" {
		t.Fatalf("code %d result %+v", code, r)
	}
}

// bundle makes a fake app bundle with a marker naming where it came from.
func (j *jobEnv) bundle(path, marker string) {
	j.t.Helper()
	os.MkdirAll(path, 0o700)
	os.WriteFile(filepath.Join(path, "marker"), []byte(marker), 0o600)
}

func (j *jobEnv) liveMarker() string {
	b, _ := os.ReadFile(filepath.Join(j.tmp, "Live.app", "marker"))
	return string(b)
}

func TestJobRestoresFromRollbackPointWhenLiveAppIsGone(t *testing.T) {
	j := newJobEnv(t)
	j.bundle(filepath.Join(j.state, "releases", "20260101T000000Z-aaa", "app", "Tallpine.app"), "old-rollback")
	j.bundle(filepath.Join(j.state, "releases", "20260301T000000Z-bbb", "app", "Tallpine.app"), "newest-rollback")
	_, r := j.run("FAKE_INSTALL_FAIL=1", "FAKE_ROLLBACK_DELETES_LIVE=1")
	if j.liveMarker() != "newest-rollback" || !j.opened() || !strings.Contains(r.Message, "rollback point") {
		t.Fatalf("marker %q opened %v result %+v", j.liveMarker(), j.opened(), r)
	}
}

func TestJobRestoresFromNewBuildWhenNoRollbackPoint(t *testing.T) {
	j := newJobEnv(t)
	_, r := j.run("FAKE_INSTALL_FAIL=1", "FAKE_ROLLBACK_DELETES_LIVE=1")
	// The fake build created an empty bundle; it must be what is installed.
	if _, err := os.Stat(filepath.Join(j.tmp, "Live.app")); err != nil || !j.opened() || !strings.Contains(r.Message, "new build") {
		t.Fatalf("live missing or not opened: %v %+v", err, r)
	}
}

func TestJobRestoresFromEarlierBuildAsLastResort(t *testing.T) {
	j := newJobEnv(t)
	j.bundle(filepath.Join(j.state, "release-builds", "earlier", "frontend", "out", "x", "Tallpine.app"), "earlier-build")
	_, r := j.run("FAKE_INSTALL_FAIL=1", "FAKE_ROLLBACK_DELETES_LIVE=1", "FAKE_ROLLBACK_DELETES_BUILD=1")
	if j.liveMarker() != "earlier-build" || !strings.Contains(r.Message, "earlier build") {
		t.Fatalf("marker %q result %+v", j.liveMarker(), r)
	}
}

func TestJobFailsLoudlyWhenNoAppCanBeStarted(t *testing.T) {
	j := newJobEnv(t)
	_, r := j.run("FAKE_INSTALL_FAIL=1", "FAKE_ROLLBACK_DELETES_LIVE=1", "FAKE_ROLLBACK_DELETES_BUILD=1")
	if r.State != "failed" || !strings.Contains(r.Message, "no app could be started") {
		t.Fatalf("result %+v", r)
	}
}

func TestJobRollbackThatLeavesDaemonDownIsNotRecordedAsRolledBack(t *testing.T) {
	j := newJobEnv(t)
	_, r := j.run("FAKE_INSTALL_FAIL=1", "FAKE_ROLLBACK_UNHEALTHY=1")
	if r.State != "failed" || !strings.Contains(r.Message, "daemon is not healthy") {
		t.Fatalf("result %+v", r)
	}
}

func TestJobEmptyHealthzCommitIsNotHealthy(t *testing.T) {
	j := newJobEnv(t)
	_, r := j.run("FAKE_EMPTY_COMMIT=1")
	if r.State != "rolled_back" || !strings.Contains(j.calls(), "rollback") {
		t.Fatalf("result %+v calls %s", r, j.calls())
	}
}

func TestJobFailedStagingKeepsThePreviousBundle(t *testing.T) {
	for name, extra := range map[string]string{"copy fails": "UPDATE_COPY_CMD=false", "verify fails": "UPDATE_VERIFY_CMD=false"} {
		t.Run(name, func(t *testing.T) {
			j := newJobEnv(t)
			j.bundle(filepath.Join(j.state, "releases", "20260101T000000Z-aaa", "app", "Tallpine.app"), "rollback")
			// Live exists but the daemon will not come up, so the tiers are tried.
			j.run("FAKE_INSTALL_FAIL=1", "FAKE_ROLLBACK_UNHEALTHY=1", extra)
			if j.liveMarker() != "live" {
				t.Fatalf("previous bundle lost: marker %q", j.liveMarker())
			}
			if m, _ := filepath.Glob(filepath.Join(j.tmp, "Live.app.*")); len(m) != 0 {
				t.Fatalf("staging debris: %v", m)
			}
		})
	}
}

// B176: the daemon has no loopback TCP by default, so the job must probe its
// socket. Port 1 answers nothing; only the socket serves the fake daemon.
func TestJobProbesTheDaemonSocket(t *testing.T) {
	j := newJobEnv(t)
	dir, err := os.MkdirTemp("", "uj") // short: sun_path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: j.handler, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	if code, r := j.run("UPDATE_PORT=1", "UPDATE_SOCKET="+sock); code != 0 || r.State != "succeeded" {
		t.Fatalf("over the socket: code %d result %+v", code, r)
	}
}
