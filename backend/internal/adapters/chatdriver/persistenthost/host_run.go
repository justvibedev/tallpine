package persistenthost

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
	"tallpine.eu/tallpine/backend/internal/processalive"
)

// Run owns the provider until it exits or an authenticated shutdown arrives.
func Run(ctx context.Context, cfg Config) error {
	if len(cfg.Argv) == 0 || !filepath.IsAbs(cfg.Workdir) {
		return errors.New("chat host requires provider argv and absolute workdir")
	}
	releaseLock, err := acquireHostLock(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return err
	}
	defer releaseLock()
	// Holding the lock proves any previous host is gone. Its descriptor is about
	// to be replaced, and with it the only record of the agent it left behind.
	reapPredecessor(ctx, cfg.DataDir, cfg.SessionID)
	token, err := randomToken()
	if err != nil {
		return err
	}
	listener, address, err := listenHost(cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	child := tallpineprocess.Command(cfg.Argv[0], cfg.Argv[1:]...) //nolint:gosec // provider argv is constructed by Tallpine's driver.
	child.Dir = cfg.Workdir
	child.Env = cfg.Env
	configureProviderProcess(child)
	stdin, err := child.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		return err
	}
	child.Stderr = io.Discard
	closeProviderTree, err := startProviderProcess(child)
	if err != nil {
		return err
	}
	// The job owns descendants even if the direct provider exits first. Close
	// it before releasing the host lock so Shutdown follows the kill request.
	defer closeProviderTree()

	d := Descriptor{
		Version: ProtocolVersion, SessionID: cfg.SessionID, Protocol: cfg.Protocol,
		OwnershipFingerprint: cfg.OwnershipFingerprint,
		Address:              address, Token: token, PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	d.HostStart, d.Provider = launchRecord(child.Process.Pid, cfg.Env)
	if err := writeDescriptor(cfg.DataDir, d); err != nil {
		_ = killProviderProcess(context.WithoutCancel(ctx), child)
		return err
	}
	path, _ := descriptorPath(cfg.DataDir, cfg.SessionID)
	defer func() { _ = os.Remove(path) }()

	h := &host{
		ctx: ctx, listener: listener, child: child, stdin: stdin, token: token,
		detached: make([][]byte, 0, 64), pendingRequests: make(map[string]*pendingRequest),
		shutdown: make(chan struct{}),
	}
	if cfg.Protocol == ProtocolACP {
		h.acp, err = newACPRelay(ctx, filepath.Join(filepath.Dir(path), "acp-prompt.journal"))
		if err != nil {
			_ = killProviderProcess(context.WithoutCancel(ctx), child)
			return err
		}
		defer func() { _ = h.acp.close(context.WithoutCancel(ctx)) }()
	}
	h.cond = sync.NewCond(&h.mu)
	providerDone := make(chan error, 1)
	go func() { providerDone <- h.forwardProvider(stdout) }()
	if h.acp != nil {
		watchCtx, stopWatch := context.WithCancel(ctx)
		defer stopWatch()
		go h.watchPromptStall(watchCtx)
	}
	acceptDone := make(chan error, 1)
	go func() { acceptDone <- h.accept() }()
	// A host outlives its daemon by design, but not its own state dir: once
	// the dir is deleted nothing can ever reconnect, so exit instead of leaking.
	go h.watchStateDir(ctx, filepath.Dir(path))

	wait := sync.OnceValue(child.Wait)
	// reapProvider waits up to a second for the provider to exit once its
	// output ended, so its zombie does not hold the process group open while
	// the group is asked to exit. A provider still running is left to the kill.
	reapProvider := func() {
		reaped := make(chan struct{})
		go func() { _ = wait(); close(reaped) }()
		select {
		case <-reaped:
		case <-time.After(time.Second):
		}
	}
	stopProvider := func() {
		_ = stdin.Close()
		deadline := time.After(providerStopDeadline)
		grace := providerStdinGrace
		if h.quickStop.Load() {
			grace = providerQuickGrace
		}
		select {
		case <-providerDone: //nolint:errcheck // only completion matters here
			// Wrapper adapters may exit before their provider child. The hosted
			// process tree is the ownership boundary, so explicit shutdown reaps
			// any descendant that did not follow stdin closure.
			reapProvider()
			_ = killProviderProcess(context.WithoutCancel(ctx), child)
			return
		case <-time.After(grace):
		}
		// Some agents (Cursor) ignore stdin closing and only exit on a signal.
		_ = terminateProviderProcess(child)
		// The provider's output can stay open after it exits when a helper it
		// started inherited the pipe, so its own exit also ends the wait.
		exited := time.NewTicker(100 * time.Millisecond)
		defer exited.Stop()
	wait:
		for {
			select {
			case <-providerDone: //nolint:errcheck // only completion matters here
				break wait
			case <-deadline:
				break wait
			case <-exited.C:
				if child.Process != nil && !processalive.Alive(child.Process.Pid) {
					break wait
				}
			}
		}
		reapProvider()
		_ = killProviderProcess(context.WithoutCancel(ctx), child)
	}
	var runErr error
	select {
	case runErr = <-providerDone:
		reapProvider()
		_ = killProviderProcess(context.WithoutCancel(ctx), child)
	case <-h.shutdown:
		stopProvider()
	case <-ctx.Done():
		runErr = ctx.Err()
		stopProvider()
	case err := <-acceptDone:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			_ = killProviderProcess(context.WithoutCancel(ctx), child)
			return err
		}
	}
	_ = listener.Close()
	_ = wait()
	return runErr
}

type host struct {
	ctx      context.Context
	listener net.Listener
	child    *exec.Cmd
	stdin    io.WriteCloser
	token    string
	acp      *acpRelay

	mu               sync.Mutex
	cond             *sync.Cond
	client           net.Conn
	clientGeneration uint64
	detached         [][]byte
	detachedBytes    int
	pendingRequests  map[string]*pendingRequest
	pendingOrder     []string
	maxRequestID     int64
	shutdown         chan struct{}
	shutdownOnce     sync.Once
	quickStop        atomic.Bool // set before shutdown closes; see ShutdownQuick
}

type pendingRequest struct {
	frame    []byte
	buffered bool
}

func (h *host) accept() error {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			return err
		}
		go h.handle(conn)
	}
}

func (h *host) handle(conn net.Conn) {
	reader := bufio.NewReader(conn)
	var request hello
	if err := json.NewDecoder(reader).Decode(&request); err != nil {
		_ = conn.Close()
		return
	}
	if request.Version != ProtocolVersion {
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: ErrIncompatible.Error()})
		_ = conn.Close()
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Token), []byte(h.token)) != 1 {
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: ErrUnauthorized.Error()})
		_ = conn.Close()
		return
	}
	if request.Action == "shutdown" {
		_ = json.NewEncoder(conn).Encode(helloResponse{OK: true})
		_ = conn.Close()
		if request.QuickStop {
			h.quickStop.Store(true)
		}
		h.shutdownOnce.Do(func() { close(h.shutdown) })
		return
	}
	if request.Action != "attach" {
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: "unknown chat host action"})
		_ = conn.Close()
		return
	}

	h.mu.Lock()
	if h.client != nil {
		h.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: ErrAttached.Error()})
		_ = conn.Close()
		return
	}
	h.client = conn
	h.clientGeneration++
	generation := h.clientGeneration
	response := helloResponse{OK: true, NextRequestID: h.maxRequestID}
	if h.acp != nil {
		response.ACPState = h.acp.snapshot()
	}
	writeErr := json.NewEncoder(conn).Encode(response)
	if writeErr == nil && h.acp != nil {
		writeErr = h.acp.replayTo(h.ctx, conn)
	}
	if writeErr == nil {
		for _, frame := range h.detached {
			if _, writeErr = conn.Write(frame); writeErr != nil {
				break
			}
		}
	}
	if writeErr == nil {
		h.detached = h.detached[:0]
		h.detachedBytes = 0
		for _, pending := range h.pendingRequests {
			pending.buffered = false
		}
		h.cond.Broadcast()
	}
	h.mu.Unlock()
	if writeErr != nil {
		h.detach(conn)
		return
	}

	for {
		frame, readErr := reader.ReadBytes('\n')
		if len(frame) > 0 {
			providerFrame := frame
			var clientFrame []byte
			h.mu.Lock()
			if h.acp != nil {
				var relayErr error
				var relayed acpClientFrames
				relayed, relayErr = h.acp.clientFrame(h.ctx, frame, generation)
				providerFrame = relayed.provider
				clientFrame = relayed.client
				if relayErr != nil {
					h.shutdownOnce.Do(func() { close(h.shutdown) })
					h.mu.Unlock()
					h.detach(conn)
					return
				}
			}
			h.mu.Unlock()
			if len(providerFrame) > 0 {
				if _, err := h.stdin.Write(providerFrame); err != nil {
					readErr = err
				} else {
					h.observeClientFrame(providerFrame)
				}
			}
			if readErr == nil && len(clientFrame) > 0 {
				if _, err := conn.Write(clientFrame); err != nil {
					readErr = err
				}
			}
		}
		if readErr != nil {
			h.detach(conn)
			return
		}
	}
}

func (h *host) observeClientFrame(frame []byte) {
	var value struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(frame, &value) != nil || len(value.ID) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if value.Method != "" {
		var id int64
		if json.Unmarshal(value.ID, &id) == nil && id > h.maxRequestID {
			h.maxRequestID = id
		}
		return
	}
	requestID := string(value.ID)
	if _, ok := h.pendingRequests[requestID]; ok {
		delete(h.pendingRequests, requestID)
		for i, id := range h.pendingOrder {
			if id == requestID {
				h.pendingOrder = append(h.pendingOrder[:i], h.pendingOrder[i+1:]...)
				break
			}
		}
	}
}

func (h *host) detach(conn net.Conn) {
	h.mu.Lock()
	if h.client == conn {
		h.client = nil
		h.bufferPendingRequestsLocked()
	}
	h.mu.Unlock()
	_ = conn.Close()
}

// deliverProviderFrameLocked routes one adapter frame to the attached
// controller, or buffers it for replay while detached. Callers must hold
// h.mu. retainedByACP marks frames the ACP relay owns for its own replay, so
// they must not also enter the generic detached buffer.
func (h *host) deliverProviderFrameLocked(frame []byte, retainedByACP bool) {
	if requestID, ok := serverRequestID(frame); ok && !retainedByACP {
		if _, exists := h.pendingRequests[requestID]; !exists {
			h.pendingRequests[requestID] = &pendingRequest{frame: append([]byte(nil), frame...)}
			h.pendingOrder = append(h.pendingOrder, requestID)
		}
	}
	for h.client == nil && h.detachedBytes+len(frame) > maxDetachedBytes {
		h.cond.Wait()
	}
	if h.client != nil {
		if _, writeErr := h.client.Write(frame); writeErr != nil {
			_ = h.client.Close()
			h.client = nil
			h.bufferPendingRequestsLocked()
			if _, pending := serverRequestID(frame); !pending && !retainedByACP {
				h.bufferFrameLocked(frame)
			}
		}
	} else if !retainedByACP {
		if requestID, pending := serverRequestID(frame); !pending || !h.pendingRequests[requestID].buffered {
			h.bufferFrameLocked(frame)
			if pending {
				h.pendingRequests[requestID].buffered = true
			}
		}
	}
}

// watchPromptStall terminates an in-flight prompt whose adapter went silent.
// Turn settlement lives inside the ACP adapter, and the host discards the
// adapter's stderr, so a lost completion (upstream claude-agent-acp#1145
// class) leaves the session's Working indicator hanging with nothing to
// observe. The watchdog gives such turns a terminal error and cancels the
// adapter's turn. It never races forwardProvider: both relay under h.mu.
func (h *host) watchPromptStall(ctx context.Context) {
	ticker := time.NewTicker(promptStallCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			frames, err := h.acp.failStalledPrompt(ctx, h.clientGeneration, h.client != nil, time.Now())
			if err == nil && len(frames.client) > 0 {
				h.deliverProviderFrameLocked(frames.client, true)
			}
			h.mu.Unlock()
			// Like client frames, written outside h.mu: a full stdin pipe must
			// not stall the provider reader.
			if len(frames.provider) > 0 {
				_, _ = h.stdin.Write(frames.provider)
			}
		}
	}
}

func (h *host) forwardProvider(stdout io.Reader) error {
	reader := bufio.NewReader(stdout)
	for {
		frame, err := reader.ReadBytes('\n')
		if len(frame) > 0 {
			h.mu.Lock()
			retainedByACP := false
			if h.acp != nil {
				var relayErr error
				frame, retainedByACP, relayErr = h.acp.providerFrame(
					h.ctx, frame, h.clientGeneration, h.client != nil,
				)
				if relayErr != nil {
					h.mu.Unlock()
					return relayErr
				}
			}
			if len(frame) == 0 {
				h.mu.Unlock()
				if err != nil {
					return err
				}
				continue
			}
			h.deliverProviderFrameLocked(frame, retainedByACP)
			h.mu.Unlock()
		}
		if err != nil {
			return err
		}
	}
}

func serverRequestID(frame []byte) (string, bool) {
	var value struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(frame, &value) != nil || len(value.ID) == 0 || value.Method == "" {
		return "", false
	}
	return string(value.ID), true
}

func (h *host) bufferPendingRequestsLocked() {
	for _, requestID := range h.pendingOrder {
		pending := h.pendingRequests[requestID]
		if pending == nil || pending.buffered {
			continue
		}
		h.bufferFrameLocked(pending.frame)
		pending.buffered = true
	}
}

func (h *host) bufferFrameLocked(frame []byte) {
	h.detached = append(h.detached, append([]byte(nil), frame...))
	h.detachedBytes += len(frame)
}
