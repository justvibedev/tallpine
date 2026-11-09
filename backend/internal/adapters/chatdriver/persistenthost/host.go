// Package persistenthost keeps a provider stdio process alive while Tallpine's daemon
// is replaced. Raw protocols are forwarded unchanged. ACP uses a small relay
// that preserves connection-scoped initialization, session, request-correlation,
// and in-flight prompt state across daemon attachments.
package persistenthost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/hostsock"
)

const (
	// ProtocolVersion fences daemon/host control-plane compatibility.
	ProtocolVersion  = 1
	maxDetachedBytes = 32 << 20
	startupTimeout   = 10 * time.Second
	// Host hello is local control-plane I/O and must not stall daemon recovery.
	handshakeTimeout = time.Second
	// A stopped provider gets providerStdinGrace (providerQuickGrace for a quick
	// stop) to exit on stdin closing, then SIGTERM, then a kill at the deadline.
	providerStdinGrace   = time.Second
	providerQuickGrace   = 300 * time.Millisecond
	providerStopDeadline = 3 * time.Second
)

// Protocol selects the provider-wire behavior owned by the host.
type Protocol string

const (
	// ProtocolRaw preserves the original newline-delimited forwarding profile.
	ProtocolRaw Protocol = ""
	// ProtocolACP enables the host-owned ACP correlation and replay profile.
	ProtocolACP Protocol = "acp"
	// ProtocolUnreal preserves Unreal Agent's Tallpine-private JSONL stream while
	// fencing ownership with the same stable launch identity used by ACP hosts.
	ProtocolUnreal Protocol = "unreal"

	// ACPPromptResultMethod is a Tallpine-private notification emitted when an ACP
	// prompt finishes after the daemon attachment that issued it has gone away.
	ACPPromptResultMethod = "_ao/persistent_prompt_result"
	// ACPPromptAckMethod acknowledges that Tallpine durably committed a replayed prompt
	// result, allowing the host to discard its prompt journal.
	ACPPromptAckMethod = "_ao/persistent_prompt_ack"
	// ACPInteractionCommandMethod records a user interaction decision in the host
	// before the daemon releases the blocked ACP responder.
	ACPInteractionCommandMethod = "_ao/persistent_interaction_command"
	// ACPRequestIDMetaKey carries one host-stable identity on replayable
	// provider-to-client requests such as permissions and elicitations.
	ACPRequestIDMetaKey = "ao.persistentRequestId"
	// ACPInitialPermissionsMetaKey records the process-launch approval policy,
	// which can differ from mutable session settings after reconnect.
	ACPInitialPermissionsMetaKey = "ao.initialPermissions"
)

var (
	// ErrAttached reports that another daemon controller owns the host.
	ErrAttached = errors.New("chat host already has a controller")
	// ErrUnauthorized reports an invalid host capability.
	ErrUnauthorized = errors.New("chat host authentication failed")
	// ErrIncompatible reports a control-plane or provider-ownership mismatch.
	ErrIncompatible = errors.New("chat host incompatible")
	// ErrHostExists reports an atomic per-session launch-lock conflict.
	ErrHostExists = errors.New("chat host already exists")
	// ErrOwnershipInconclusive means a descriptor or live host exists, but this
	// client could not prove that it is safe to replace. Callers must preserve the
	// durable session rather than treating the failed attachment as provider death.
	ErrOwnershipInconclusive = errors.New("chat host ownership is inconclusive")
	// ErrNotRunning means a reconnect-only probe found no surviving host.
	ErrNotRunning = errors.New("chat host is not running")
)

// Descriptor is the private connection record published by a running host.
type Descriptor struct {
	Version              int       `json:"version"`
	SessionID            string    `json:"sessionId"`
	Protocol             Protocol  `json:"protocol,omitempty"`
	OwnershipFingerprint string    `json:"ownershipFingerprint,omitempty"`
	Address              string    `json:"address"`
	Token                string    `json:"token"`
	PID                  int       `json:"pid"`
	StartedAt            time.Time `json:"startedAt"`
	// HostStart is the host process's OS start time, which tells it from an
	// unrelated process that reused PID. Zero when it could not be read.
	HostStart time.Time `json:"hostStart,omitzero"`
	// Provider identifies the process the host owns, so a daemon can stop it
	// if the host is killed before it can do so itself.
	Provider *ProviderRecord `json:"provider,omitempty"`
}

// Config identifies one provider process and its Tallpine session ownership.
type Config struct {
	ReconnectOnly        bool
	SessionID            string
	DataDir              string
	Workdir              string
	Env                  []string
	Argv                 []string
	Protocol             Protocol
	OwnershipFingerprint string
	Prepare              func(context.Context) (PreparedProvider, error)
}

// PreparedProvider is resolved only when no compatible live host can be
// adopted. This keeps launch-only credential rotation out of reconnects.
type PreparedProvider struct {
	Env  []string
	Argv []string
}

// ACPState is the connection-scoped state needed to reconstruct an ACP client
// without initializing or resuming the same live provider a second time.
type ACPState struct {
	InitialPermissions   string          `json:"initialPermissions,omitempty"`
	InitializeResult     json.RawMessage `json:"initializeResult,omitempty"`
	SessionResult        json.RawMessage `json:"sessionResult,omitempty"`
	SessionID            string          `json:"sessionId,omitempty"`
	ActivePrompt         bool            `json:"activePrompt,omitempty"`
	ActiveCompaction     bool            `json:"activeCompaction,omitempty"`
	PendingResultEventID string          `json:"pendingResultEventId,omitempty"`
}

// Transport is one authenticated attachment to a persistent provider host.
type Transport struct {
	Stdin         io.WriteCloser
	Stdout        io.Reader
	Reconnected   bool
	NextRequestID int64
	ACPState      *ACPState
}

type hello struct {
	Version int    `json:"version"`
	Token   string `json:"token"`
	Action  string `json:"action"`
	// QuickStop on a shutdown: see ShutdownQuick.
	QuickStop bool `json:"quickStop,omitempty"`
}

type helloResponse struct {
	OK            bool      `json:"ok"`
	Error         string    `json:"error,omitempty"`
	NextRequestID int64     `json:"nextRequestId,omitempty"`
	ACPState      *ACPState `json:"acpState,omitempty"`
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validateDescriptor(cfg Config, d Descriptor) error {
	if d.Protocol != cfg.Protocol {
		return fmt.Errorf("%w: host protocol=%q requested=%q", ErrIncompatible, d.Protocol, cfg.Protocol)
	}
	// Codex raw hosts predate this ACP compatibility contract and retain their
	// existing version/protocol fencing.
	if d.Protocol == ProtocolRaw {
		return nil
	}
	if cfg.OwnershipFingerprint == "" || d.OwnershipFingerprint != cfg.OwnershipFingerprint {
		return fmt.Errorf("%w: provider launch configuration changed", ErrIncompatible)
	}
	return nil
}

// hostArgs keeps the Unix and Windows detached entry points on one wire format.
func hostArgs(cfg Config) []string {
	args := []string{"chat-host", cfg.SessionID, cfg.DataDir, cfg.Workdir}
	if cfg.Protocol != ProtocolRaw {
		args = append(args, string(cfg.Protocol), cfg.OwnershipFingerprint)
	}
	return append(append(args, "--"), cfg.Argv...)
}

// dialHost connects to a descriptor address: a "unix:" socket for current
// hosts, or a loopback TCP "host:port" published by a host from an older build.
func dialHost(ctx context.Context, address string) (net.Conn, error) {
	if hostsock.IsUnix(address) {
		return hostsock.Dial(ctx, address, 0)
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", address)
}

// listenHost binds the chat host endpoint: a private Unix socket under the data
// dir where supported, loopback TCP only on Windows.
func listenHost(dataDir string) (net.Listener, string, error) {
	if hostsock.Supported {
		return hostsock.Listen(filepath.Join(dataDir, hostsock.DirName))
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	return ln, ln.Addr().String(), nil
}
