//go:build darwin || linux

package peeradmit

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

// The test binary re-executes itself in these roles so each peer is a real
// separate process connecting to a real socket.
const (
	helperRoleEnv   = "PEERADMIT_HELPER_ROLE"
	helperSocketEnv = "PEERADMIT_HELPER_SOCKET"
	helperKindEnv   = "PEERADMIT_HELPER_KIND"
	helperParentEnv = "PEERADMIT_HELPER_PARENT"

	roleClient       = "client"
	roleAbandon      = "abandon"
	roleTreeRoot     = "tree-root"
	roleSessionRoot  = "session-root"
	roleIntermediate = "intermediate"
	roleOrphanClient = "orphan-client"
)

func runHelper(role string) {
	switch role {
	case roleClient:
		sendRequest(os.Getenv(helperSocketEnv), os.Getenv(helperKindEnv))
	case roleAbandon:
		abandonRequest()
	case roleTreeRoot:
		child := helperCommand(roleClient)
		child.Stdout = os.Stdout
		runOrDie(child)
		_, _ = io.Copy(io.Discard, os.Stdin) // keep this root alive until told
	case roleSessionRoot:
		runOrDie(helperCommand(roleIntermediate))
		_, _ = io.Copy(io.Discard, os.Stdin)
	case roleIntermediate:
		// Start the client and exit at once, orphaning it inside the session.
		child := helperCommand(roleOrphanClient)
		child.Env = append(child.Env, helperParentEnv+"="+strconv.Itoa(os.Getpid()))
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case roleOrphanClient:
		parent, _ := strconv.Atoi(os.Getenv(helperParentEnv))
		for i := 0; os.Getppid() == parent && i < 250; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		sendRequest(os.Getenv(helperSocketEnv), os.Getenv(helperKindEnv))
	default:
		fmt.Fprintln(os.Stderr, "unknown helper role", role)
		os.Exit(2)
	}
	os.Exit(0)
}

func helperCommand(role string) *exec.Cmd {
	cmd := exec.Command(os.Args[0]) //nolint:forbidigo,gosec // the test binary re-executing itself
	cmd.Env = append(os.Environ(), helperRoleEnv+"="+role)
	return cmd
}

func runOrDie(cmd *exec.Cmd) {
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func sendRequest(socket, kind string) {
	client := http.Client{Transport: daemonsock.Transport(socket), Timeout: 10 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "http://daemon/probe", http.NoBody)
	req.Header.Set("X-Kind", kind)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = resp.Body.Close()
}

// abandonRequest writes a request and exits without waiting for an answer.
func abandonRequest() {
	conn, err := net.Dial("unix", os.Getenv(helperSocketEnv))
	if err != nil {
		os.Exit(1)
	}
	_, _ = fmt.Fprintf(conn, "GET /probe HTTP/1.1\r\nHost: daemon\r\nX-Kind: %s\r\n\r\n", os.Getenv(helperKindEnv))
	time.Sleep(200 * time.Millisecond) // long enough for the server to accept
	os.Exit(0)
}

// socketServer serves real connections and feeds every request to a Checker
// the way daemonAuthMiddleware does.
type socketServer struct {
	path    string
	checker *Checker
	gate    chan struct{}
	done    chan struct{}
	server  *http.Server
}

func startSocketServer(t *testing.T, path string, checker *Checker, gate chan struct{}) *socketServer {
	t.Helper()
	listener, err := daemonsock.Listen(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := &socketServer{path: path, checker: checker, gate: gate, done: make(chan struct{})}
	s.server = &http.Server{
		ConnContext: daemonsock.ContextWithConn,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if gate != nil {
				<-gate
			}
			peer, _ := daemonsock.PeerFrom(r.Context())
			credential := Credential{Kind: CredentialKind(r.Header.Get("X-Kind")), SessionID: "s1"}
			checker.Observe(Observation{Peer: peer, Credential: credential, Method: r.Method, Path: r.URL.Path})
			w.WriteHeader(http.StatusNoContent)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { defer close(s.done); _ = s.server.Serve(listener) }()
	t.Cleanup(s.stop)
	return s
}

func (s *socketServer) stop() {
	_ = s.server.Close()
	<-s.done
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(testenv.ShortBaseDir(), "pa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "sock", "daemon.sock")
}

func realChecker(t *testing.T, roots RootResolver) *Checker {
	t.Helper()
	c := New(Options{Mode: ModeReport, Roots: roots, Verifier: &fakeVerifier{result: SignatureOther}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)
	return c
}

func waitForRecord(t *testing.T, c *Checker) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		flush(t, c)
		report := c.Report()
		if len(report.WouldRefuse) > 0 || sum(report.Admitted) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no classification recorded")
}

func sum(counts map[Class]int64) int64 {
	var total int64
	for _, n := range counts {
		total += n
	}
	return total
}

func helperEnv(socket string, kind CredentialKind) []string {
	return []string{helperSocketEnv + "=" + socket, helperKindEnv + "=" + string(kind)}
}

// startHelper starts a helper whose stdin keeps it alive; closing stdin ends it.
func startHelper(t *testing.T, role string, env []string, newSession bool) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cmd := helperCommand(role)
	cmd.Env = append(cmd.Env, env...)
	cmd.Stderr = os.Stderr
	if newSession {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd, stdin
}

type lazyRoots struct{ agent, shell func() []int }

func (l lazyRoots) AgentRoots(context.Context, Credential) ([]int, error) { return l.agent(), nil }
func (l lazyRoots) ShellRoots(context.Context) ([]int, error)             { return l.shell(), nil }

func noRoots() []int { return nil }

func TestRealSocketDescendantAdmitted(t *testing.T) {
	path := shortSocketPath(t)
	var root int
	c := realChecker(t, lazyRoots{agent: func() []int { return []int{root} }, shell: noRoots})
	startSocketServer(t, path, c, nil)
	cmd, _ := startHelper(t, roleTreeRoot, helperEnv(path, CredentialSession), false)
	root = cmd.Process.Pid
	waitForRecord(t, c)
	if report := c.Report(); report.Admitted[ClassAgent] != 1 || len(report.WouldRefuse) != 0 {
		t.Fatalf("descendant of the root must be admitted: %+v", report)
	}
}

func TestRealSocketSameSessionAdmittedAfterParentExits(t *testing.T) {
	path := shortSocketPath(t)
	var root int
	c := realChecker(t, lazyRoots{agent: func() []int { return []int{root} }, shell: noRoots})
	startSocketServer(t, path, c, nil)
	cmd, _ := startHelper(t, roleSessionRoot, helperEnv(path, CredentialSession), true)
	root = cmd.Process.Pid
	waitForRecord(t, c)
	if report := c.Report(); report.Admitted[ClassAgent] != 1 || len(report.WouldRefuse) != 0 {
		t.Fatalf("orphan in the root's POSIX session must be admitted: %+v", report)
	}
}

func TestRealSocketUnrelatedProcessWithValidTokenNotAdmitted(t *testing.T) {
	path := shortSocketPath(t)
	var root int
	c := realChecker(t, lazyRoots{agent: noRoots, shell: func() []int { return []int{root} }})
	startSocketServer(t, path, c, nil)
	// A live, leading root that the unrelated client has no connection to.
	rootCmd, _ := startHelper(t, roleSessionRoot, helperEnv(filepath.Join(filepath.Dir(path), "none"), CredentialNone), true)
	root = rootCmd.Process.Pid
	cmd, _ := startHelper(t, roleClient, helperEnv(path, CredentialInstall), false)
	_ = cmd
	waitForRecord(t, c)
	report := c.Report()
	if len(report.WouldRefuse) != 1 || report.WouldRefuse[0].Reason != ReasonInstallTokenOutside || sum(report.Admitted) != 0 {
		t.Fatalf("unrelated peer must be not admitted: %+v", report)
	}
	if !strings.Contains(report.WouldRefuse[0].Executable, filepath.Base(os.Args[0])) {
		t.Fatalf("record must name the executable: %+v", report.WouldRefuse[0])
	}
}

func TestRealSocketExitedPeerFailsClosed(t *testing.T) {
	path := shortSocketPath(t)
	gate := make(chan struct{})
	c := realChecker(t, lazyRoots{agent: func() []int { return []int{os.Getpid()} }, shell: noRoots})
	startSocketServer(t, path, c, gate)
	cmd := helperCommand(roleAbandon)
	cmd.Env = append(cmd.Env, helperEnv(path, CredentialSession)...)
	if err := cmd.Run(); err != nil { // Run reaps it, so its pid is gone.
		t.Fatal(err)
	}
	close(gate)
	waitForRecord(t, c)
	report := c.Report()
	if len(report.WouldRefuse) != 1 || report.WouldRefuse[0].Reason != ReasonPeerExited {
		t.Fatalf("exited peer must fail closed: %+v", report)
	}
}

func TestRealSocketPeerFacts(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := daemonsock.Listen(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn := <-accepted
	defer conn.Close()
	peer, ok := daemonsock.PeerFrom(daemonsock.ContextWithConn(context.Background(), conn))
	if !ok || peer.Transport != daemonsock.TransportUnix || peer.PID != os.Getpid() || peer.Err != "" {
		t.Fatalf("peer = %+v (pid %s)", peer, strconv.Itoa(os.Getpid()))
	}
}
