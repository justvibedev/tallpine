package backlog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ErrNoOrchestrator is what a Notifier returns when the project has no live
// orchestrator to tell.
var ErrNoOrchestrator = errors.New("no live orchestrator")

// Notifier posts one message to the project's orchestrator chat.
type Notifier interface {
	NotifyOrchestrator(ctx context.Context, projectID domain.ProjectID, message string) error
}

// PlanNotifier posts plan lifecycle notices. target is the orchestrator that
// started the plan; when it is empty or no longer live the notice goes to the
// project's main orchestrator. Notices are system messages, never the owner's.
type PlanNotifier interface {
	NotifyPlanOrchestrator(ctx context.Context, projectID domain.ProjectID, target domain.SessionID, message string) error
}

// runRequestedText is the single message sent when the owner presses Run. It
// carries everything the orchestrator needs to start without a system-prompt rule.
func runRequestedText(run domain.BacklogRun) string {
	return fmt.Sprintf("Backlog run %s requested (scope %s, %s). Plan it, then run it, following the \"Run mode\" section of the backlog guide (tallpine backlog run --help). The run's state lives in `tallpine backlog run status %s`; check it at the start of every turn while the run is open.",
		run.Ref(), run.Scope, describeSelection(run), run.Ref())
}

// runControlText is the nudge sent when the owner pauses, resumes or stops a
// run from the UI: the state change is durable, this just wakes the orchestrator.
func runControlText(run domain.BacklogRun, action string) string {
	switch action {
	case "stop":
		return fmt.Sprintf("Owner pressed Stop on backlog run %s: finish the work already in flight, start nothing new. Check `tallpine backlog run status %s`.", run.Ref(), run.Ref())
	case "pause":
		return fmt.Sprintf("Owner paused backlog run %s: start nothing new until it is resumed. Check `tallpine backlog run status %s`.", run.Ref(), run.Ref())
	}
	return fmt.Sprintf("Owner resumed backlog run %s: carry on with the plan. Check `tallpine backlog run status %s`.", run.Ref(), run.Ref())
}

func describeSelection(run domain.BacklogRun) string {
	switch run.SelectionKind {
	case domain.BacklogRunSelectParent:
		return "everything under " + strings.Join(run.Selection, ", ")
	case domain.BacklogRunSelectRefs:
		return "items " + strings.Join(run.Selection, ", ")
	}
	return "all open items"
}
