package domain

import "strings"

// Client message id prefixes of queued prompts that keep their own delivery
// boundary: coordination sends and worker reports are merged, refreshed or
// superseded by the chat service's worker-queue rules at dispatch time, so
// pending-message batching and queue-ahead ordering leave them alone.
const (
	WorkerSendClientMessagePrefix  = "worker-send:"
	ReportBatchClientMessagePrefix = "report-batch:"
)

// Delivery modes recorded on a queued message that joined another turn's input.
const (
	DeliveryModeSteered = "steered"
	DeliveryModeBatched = "batched"
)

// QueueBoundaryProtected reports whether a queued prompt keeps its own boundary.
func QueueBoundaryProtected(clientMessageID string) bool {
	return strings.HasPrefix(clientMessageID, WorkerSendClientMessagePrefix) ||
		strings.HasPrefix(clientMessageID, ReportBatchClientMessagePrefix)
}
