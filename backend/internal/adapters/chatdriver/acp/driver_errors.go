package acp

import (
	"errors"
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// isACPAuthRequired reports whether err is the agent telling us the credential
// was refused, so the caller can raise a reauth prompt instead of a generic
// turn failure.
func isACPAuthRequired(err error) bool {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	return requestErr.Code == -32000
}

// acpResourceNotFound is ACP's JSON-RPC "Resource not found" error code. A
// session/load or session/resume for an id the agent never persisted returns it.
const acpResourceNotFound = -32002

func isACPResourceNotFound(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == acpResourceNotFound
}

// isACPMethodNotFound reports whether err is a JSON-RPC -32601 "Method not
// found" from the ACP agent. Agents that implement a subset of the protocol
// (e.g. no session/set_mode or session/set_config_option) return this code;
// the driver treats those calls as optional so a minimal agent can still run.
func isACPMethodNotFound(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32601
}

func isACPInvalidParams(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32602
}

// isACPInternalError reports whether err is a JSON-RPC -32603 "Internal error"
// from the ACP agent. The SDK coerces any handler error the agent did not shape
// itself into this code, so it is how a provider reports its own failure (for
// example Claude Code's own "context deadline exceeded" while replaying a
// transcript) rather than a protocol or authentication problem.
func isACPInternalError(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32603
}

// isACPRequestCancelled reports ACP's -32800 "Request cancelled", which the
// agent answers with when a prompt is cancelled (session kill or interrupt).
// It is a deliberate stop, not a provider failure.
func isACPRequestCancelled(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32800
}

func normalizeACPError(operation string, err error) error {
	if isACPAuthRequired(err) {
		return fmt.Errorf("%w: %s: %w", ports.ErrChatAuthRequired, operation, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// normalizeACPLoadError normalizes a failed ACP session/load whose calling
// context is still alive. A -32603 answer then means the provider itself could
// not replay the transcript; it is mapped to ports.ErrChatHistoryLoadFailed so
// the settle loop stops re-sending the same load and the interface transition
// reports a dedicated code instead of a generic resume failure. Callers must
// check their own context first: the SDK also synthesizes -32603 when the
// caller's context ends mid-request.
func normalizeACPLoadError(operation string, err error) error {
	if isACPInternalError(err) && !isACPAuthRequired(err) {
		return fmt.Errorf("%w: %s: %w", ports.ErrChatHistoryLoadFailed, operation, err)
	}
	return normalizeACPError(operation, err)
}
