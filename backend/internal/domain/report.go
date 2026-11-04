package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const reportDeliveryPrefix = `<ao-report-delivery id="`

// WrapReportDelivery marks a Tallpine-authored report batch so provider prompt
// hooks can correlate semantic acceptance with its durable delivery identity.
func WrapReportDelivery(id, message string) string {
	return fmt.Sprintf("%s%s\">\n%s\n</ao-report-delivery>", reportDeliveryPrefix, id, message)
}

// ReportDeliveryID returns the durable identity from a Tallpine report envelope.
// Delivery ids are generated internally and deliberately restricted so a
// malformed prompt cannot manufacture markup or an unbounded correlation key.
func ReportDeliveryID(message string) (string, bool) {
	return deliveryID(message, reportDeliveryPrefix)
}

func deliveryID(message, prefix string) (string, bool) {
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(message, prefix)
	end := strings.Index(rest, `">`)
	if end <= 0 || end > 128 {
		return "", false
	}
	id := rest[:end]
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != ':' && r != '-' && r != '_' {
			return "", false
		}
	}
	return id, true
}

const (
	// MaxReportTextCharacters is the hard limit for free-form messages and structured notes.
	MaxReportTextCharacters = 1000
	// ReportSettlementWindow gives the SCM observer time before a missing PR
	// can annotate a done report. It does not delay delivery.
	ReportSettlementWindow = 5 * time.Minute
)

// ReportState is an optional worker-originated coordination claim.
type ReportState string

// Supported report states.
const (
	ReportCheckpoint ReportState = "checkpoint"
	ReportNeedsInput ReportState = "needs_input"
	ReportStuck      ReportState = "stuck"
	ReportDone       ReportState = "done"
)

// Valid reports whether s is a supported structured state.
func (s ReportState) Valid() bool {
	switch s {
	case ReportCheckpoint, ReportNeedsInput, ReportStuck, ReportDone:
		return true
	default:
		return false
	}
}

// ReportOutputKind identifies a durable output reference attached to a report.
type ReportOutputKind string

// Supported report output kinds.
const (
	ReportOutputArtifact   ReportOutputKind = "artifact"
	ReportOutputPRCreated  ReportOutputKind = "pr_created"
	ReportOutputPRReviewed ReportOutputKind = "pr_reviewed"
)

// Valid reports whether k is a supported output kind.
func (k ReportOutputKind) Valid() bool {
	switch k {
	case ReportOutputArtifact, ReportOutputPRCreated, ReportOutputPRReviewed:
		return true
	default:
		return false
	}
}

// ReportOutput is an ordered reference to work produced by a worker.
type ReportOutput struct {
	Kind      ReportOutputKind
	Reference string
	Label     string
}

// ReportDeliveryState is the durable outbox lifecycle of a report.
type ReportDeliveryState string

// Report delivery states.
const (
	ReportPending      ReportDeliveryState = "pending"
	ReportClaimed      ReportDeliveryState = "claimed"
	ReportAcknowledged ReportDeliveryState = "acknowledged"
)

// ReportDroppedClaimToken marks a report Tallpine settled without delivering it,
// because its project was archived. LastError holds the reason.
const ReportDroppedClaimToken = "dropped:project-archived"

// ReportStaleClaimToken marks a report Tallpine settled at delivery time because it
// no longer mattered: its PR was already merged or closed. LastError holds the
// reason; the row stays in history.
const ReportStaleClaimToken = "dropped:stale-report"

// ReportDeliveryBusyReason identifies an availability retry, not a delivery failure.
const ReportDeliveryBusyReason = "orchestrator cannot take reports right now"

// ErrDuplicateReport is returned when a report with the same ID already
// exists. Callers that choose a stable ID treat it as already created.
var ErrDuplicateReport = errors.New("domain: report already exists")

// ReportRecord is the durable report and outbox persistence shape. State and
// outputs are independent, and neither changes authoritative session state.
type ReportRecord struct {
	// TurnFallbackID is an internal, transactionally consumed completion intent.
	TurnFallbackID string `json:"-"`

	ID        string
	SessionID SessionID
	ProjectID ProjectID
	// SessionDisplayName is resolved at delivery time for presentation. It is
	// intentionally not persisted with the durable report row.
	SessionDisplayName string
	// UnverifiedReason is resolved at delivery time, like SessionDisplayName, and
	// is not persisted. Non-empty means a done report has no pushed PR behind it.
	UnverifiedReason string
	// Synthesized marks a daemon-authored notice filed on the worker's behalf
	// (for example a failed Tallpine review). It is set on creation only and is not
	// persisted.
	Synthesized bool
	// Deferred reports what happened to the `--defer` lines filed with this
	// report. Set on creation only; not persisted.
	Deferred []DeferOutcome
	// Quiet marks an FYI held until its bounded deadline (AvailableAt) unless an
	// actionable delivery or the orchestrator's next turn carries it sooner.
	Quiet              bool
	State              ReportState
	Note               string
	Message            string
	Outputs            []ReportOutput
	CreatedAt          time.Time
	DeliveryState      ReportDeliveryState
	AvailableAt        time.Time
	SettlementDeadline time.Time
	RepeatCount        int64
	ClaimToken         string
	ClaimedAt          time.Time
	DeliveryAttempts   int64
	AcknowledgedAt     time.Time
	LastError          string
	DeliveryBatchID    string
}

// UndeliveredReason is why Tallpine settled this report without delivering it, or
// "" for a report that was delivered or is still pending.
func (r ReportRecord) UndeliveredReason() string {
	if r.DeliveryState != ReportAcknowledged || (r.ClaimToken != ReportDroppedClaimToken && r.ClaimToken != ReportStaleClaimToken) {
		return ""
	}
	return r.LastError
}

// ErrInvalidReport reports an invalid report or delivery transition input.
var ErrInvalidReport = errors.New("invalid report")

// Validate checks content, ownership, output, and delivery-state invariants.
func (r ReportRecord) Validate() error {
	if r.ID == "" || r.SessionID == "" || r.ProjectID == "" || r.CreatedAt.IsZero() || r.AvailableAt.IsZero() || r.RepeatCount < 1 {
		return ErrInvalidReport
	}
	if err := ValidateReportContent(r.State, r.Note, r.Message, r.Outputs); err != nil {
		return err
	}
	if r.State == ReportDone {
		if r.SettlementDeadline.IsZero() {
			return ErrInvalidReport
		}
	} else if !r.SettlementDeadline.IsZero() {
		return ErrInvalidReport
	}
	switch r.DeliveryState {
	case ReportPending:
		if r.ClaimToken != "" || !r.ClaimedAt.IsZero() || !r.AcknowledgedAt.IsZero() {
			return ErrInvalidReport
		}
	case ReportClaimed:
		if r.ClaimToken == "" || r.ClaimedAt.IsZero() || !r.AcknowledgedAt.IsZero() {
			return ErrInvalidReport
		}
	case ReportAcknowledged:
		if r.ClaimToken == "" || r.ClaimedAt.IsZero() || r.AcknowledgedAt.IsZero() {
			return ErrInvalidReport
		}
	default:
		return ErrInvalidReport
	}
	if r.DeliveryAttempts < 0 {
		return ErrInvalidReport
	}
	return nil
}

// ValidateReportContent checks the authoritative report payload contract.
func ValidateReportContent(state ReportState, note, message string, outputs []ReportOutput) error {
	if message != "" {
		if state != "" || note != "" || len(outputs) != 0 || strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > MaxReportTextCharacters {
			return ErrInvalidReport
		}
		return nil
	}
	if state == "" {
		if note != "" || len(outputs) == 0 {
			return ErrInvalidReport
		}
	} else if !state.Valid() || strings.TrimSpace(note) == "" || utf8.RuneCountInString(note) > MaxReportTextCharacters {
		return ErrInvalidReport
	}
	for _, output := range outputs {
		if !output.Kind.Valid() || strings.TrimSpace(output.Reference) == "" {
			return ErrInvalidReport
		}
		if output.Kind == ReportOutputPRCreated || output.Kind == ReportOutputPRReviewed {
			if _, err := ParseChangeRequestURL(output.Reference); err != nil {
				return ErrInvalidReport
			}
		}
	}
	return nil
}

// WrapSessionDelivery preserves Tallpine session-origin direction as coordination in
// terminal prompt hooks, without labeling it as a human prompt or worker report.
func WrapSessionDelivery(id, message string) string {
	return fmt.Sprintf("<ao-session-delivery id=%q>\n%s\n</ao-session-delivery>", id, message)
}

// CoordinationDeliveryID recognizes both durable reports and session sends.
func CoordinationDeliveryID(message string) (string, bool) {
	if id, ok := ReportDeliveryID(message); ok {
		return id, true
	}
	return deliveryID(message, `<ao-session-delivery id="`)
}

// CoordinationDeliveryBody returns the message inside a report or session
// delivery envelope, as the agent was asked it. It reports false for any other
// prompt, including Tallpine's handoff and continuation coordination.
func CoordinationDeliveryBody(message string) (string, bool) {
	if _, ok := CoordinationDeliveryID(message); !ok {
		return "", false
	}
	message = strings.TrimSpace(message)
	open := strings.Index(message, ">")
	if open < 0 {
		return "", false
	}
	body := message[open+1:]
	for _, closing := range []string{"</ao-report-delivery>", "</ao-session-delivery>"} {
		if trimmed, found := strings.CutSuffix(body, closing); found {
			body = trimmed
			break
		}
	}
	return strings.TrimSpace(body), true
}

// ReviewApprovedReportIDPrefix starts the ID of the synthesized report that
// announces a Tallpine review approval. The turn that follows it only
// acknowledges the approval, so turn-end notification treats it as covered.
const ReviewApprovedReportIDPrefix = "rpt_review_approved_"
