package chat

import "errors"

// ErrNoActiveTurn reports an interrupt with nothing to cancel.
var ErrNoActiveTurn = errors.New("no active turn")

// ErrControllerHandoff reports a send attempted after a controller handoff has
// closed source intake. The client should retain the draft and retry after the
// session_updated event announces the target mode.
var ErrControllerHandoff = errors.New("chat controller is switching interfaces")

// ErrTurnNotRetryable reports a turn that cannot be retried: it is not part of
// this conversation, not failed, was rolled back, or carries no durable prompt
// the user authored. A retry is only ever offered for an eligible failed human
// turn.
var ErrTurnNotRetryable = errors.New("turn is not retryable")

// ErrRetryStaleBranch reports a durable failed turn that is no longer visible
// on the conversation's active branch. Retrying it would inject abandoned
// history into the provider thread the user is currently viewing.
var ErrRetryStaleBranch = errors.New("turn is not on the active conversation branch")

// ErrRetryDeliveryUncertain reports a failed turn whose dispatch was never
// acknowledged by the provider. Settling it failed was Tallpine's safe guess rather
// than a fact about delivery: the provider may have accepted the work. Because
// a retry re-sends the prompt as a new turn, an uncertain source could execute
// it twice, so these turns are refused until delivery is confirmed.
var ErrRetryDeliveryUncertain = errors.New("turn was never confirmed as delivered to the provider")

// ErrRetryContentInvalid reports durable structured prompt content that can no
// longer be reconstructed safely. Retrying must fail clearly rather than drop
// an attachment or hand corrupt data to the provider.
var ErrRetryContentInvalid = errors.New("stored retry content is invalid")

// ErrRetryUnsupported reports structured prompt content the current provider
// cannot accept. This can happen after an in-place agent switch: the failed
// prompt remains visible, but the new provider may negotiate fewer capabilities.
var ErrRetryUnsupported = errors.New("current agent cannot retry this prompt content")
