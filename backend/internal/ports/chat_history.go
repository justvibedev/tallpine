package ports

import (
	"context"
	"errors"
	"fmt"
)

// ChatHistoryMismatchDimension identifies the exact durable checkpoint fact a
// provider replay has not reached. Callers may offer provider-history recovery
// only when every dimension is legacy hook text; trusted text and Tallpine high-water
// facts remain hard gates.
type ChatHistoryMismatchDimension string

// Chat history mismatch dimensions.
const (
	ChatHistoryMismatchUntrustedUserText      ChatHistoryMismatchDimension = "untrusted_user_text"
	ChatHistoryMismatchUntrustedAssistantText ChatHistoryMismatchDimension = "untrusted_assistant_text"
	ChatHistoryMismatchTrustedUserText        ChatHistoryMismatchDimension = "trusted_user_text"
	ChatHistoryMismatchTrustedTurn            ChatHistoryMismatchDimension = "trusted_turn_boundary"
	ChatHistoryMismatchTrustedAssistantText   ChatHistoryMismatchDimension = "trusted_assistant_text"
	ChatHistoryMismatchNativeIdentity         ChatHistoryMismatchDimension = "native_identity"
	ChatHistoryMismatchTallpineHighWater      ChatHistoryMismatchDimension = "ao_high_water"
	ChatHistoryMismatchUnsettledBoundary      ChatHistoryMismatchDimension = "unsettled_turn_boundary"
)

// ChatHistoryUnsettledError preserves mismatch dimensions while unwrapping to
// ErrChatHistoryUnsettled for existing admission and API handling.
type ChatHistoryUnsettledError struct {
	Dimensions []ChatHistoryMismatchDimension
}

func (e *ChatHistoryUnsettledError) Error() string {
	return fmt.Sprintf("%s: checkpoint mismatches %v", ErrChatHistoryUnsettled, e.Dimensions)
}

func (e *ChatHistoryUnsettledError) Unwrap() error { return ErrChatHistoryUnsettled }

// ChatHistoryMismatchDimensions returns a copy of typed checkpoint dimensions.
func ChatHistoryMismatchDimensions(err error) []ChatHistoryMismatchDimension {
	var mismatch *ChatHistoryUnsettledError
	if !errors.As(err, &mismatch) {
		return nil
	}
	return append([]ChatHistoryMismatchDimension(nil), mismatch.Dimensions...)
}

// ChatHistoryMismatchOnlyUntrustedText reports whether an explicit provider-
// history recovery may safely waive every mismatch in err.
func ChatHistoryMismatchOnlyUntrustedText(err error) bool {
	dimensions := ChatHistoryMismatchDimensions(err)
	if len(dimensions) == 0 {
		return false
	}
	for _, dimension := range dimensions {
		if dimension != ChatHistoryMismatchUntrustedUserText &&
			dimension != ChatHistoryMismatchUntrustedAssistantText {
			return false
		}
	}
	return true
}

// ChatHistoryReader is optionally implemented by a conversation whose native
// protocol can read the durable thread it resumed. Events are returned oldest
// first, settled, and with stable ProviderEventID values so importing the same
// native history after every interface switch is idempotent.
//
// This is deliberately provider-neutral. Codex implements it with thread/read;
// ACP implements it with session/load replay. Neither leaks provider wire formats
// into the Chat service.
type ChatHistoryReader interface {
	ReadHistory(ctx context.Context) ([]ChatEvent, error)
}

// ChatHistoryRefresher refines ChatHistoryReader for a provider that can make a
// new authoritative history observation. RefreshHistory must perform provider
// I/O or wait for a real provider signal; returning the previous capture does not
// qualify. Callers may use it for bounded convergence after an unsettled read.
type ChatHistoryRefresher interface {
	ChatHistoryReader
	RefreshHistory(ctx context.Context) ([]ChatEvent, error)
}
