package backlog

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// ownerAnsweredAsk finds an ask the owner answered themselves (a tap on the
// strip or card) after the plan last changed. id is "<ask ref>:<option id>" (Q2:build-anyway)
// or "last:<option id>" for the newest such ask about this plan's item (a named ask may have no About key, but not another item's). The
// caller names the approving option and the recorded answer must be that
// option: free text, or another option such as a re-plan, refuses. The result
// is "ask:<ref>". An answer an orchestrator relayed never counts: its message
// may predate the plan, and a fresh owner message can be passed directly.
func (p *PlanService) ownerAnsweredAsk(ctx context.Context, plan domain.BacklogPlan, id string) (string, bool, error) {
	base, opt, _ := strings.Cut(strings.TrimSpace(id), ":")
	asks, err := p.store.ListOwnerAsks(ctx, plan.ProjectID)
	if err != nil {
		return "", false, err
	}
	ref := p.itemRef(ctx, plan)
	var match *domain.OwnerAsk
	for i := range asks {
		a := &asks[i]
		if base == "last" {
			if a.State == domain.AskAnswered && a.Answer != nil && !a.Answer.Relayed && a.AnsweredAt.After(plan.UpdatedAt) &&
				strings.EqualFold(a.About, ref) && (match == nil || a.AnsweredAt.After(match.AnsweredAt)) {
				match = a
			}
		} else if strings.EqualFold(base, a.Ref) {
			// A named ask is the caller's deliberate pick, so say why it does not count.
			if err := explicitAskRefusal(*a, plan, ref); err != nil {
				return "", false, err
			}
			match = a
			break
		}
	}
	if match == nil {
		return "", false, nil
	}
	if opt == "" {
		return "", false, apierr.Forbidden("PLAN_OWNER_ASK_OPTION_REQUIRED", "Name the option the owner picked to approve: --owner-message "+match.Ref+":<option id>")
	}
	if match.Answer.OptionID != opt {
		return "", false, apierr.Forbidden("PLAN_OWNER_ASK_OPTION_MISMATCH", "The owner answered "+match.Ref+" with "+answerLabel(match.Answer)+", not option "+opt+": that does not approve")
	}
	return "ask:" + match.Ref, true, nil
}

// answerLabel names a recorded answer for an error: its option id, or "free text".
func answerLabel(a *domain.AskAnswer) string {
	if a.OptionID == "" {
		return "free text"
	}
	return "option " + a.OptionID
}

// explicitAskRefusal says why a named ask cannot back this plan, or nil when it can.
// An ask with no About key is accepted; one about another item is not.
func explicitAskRefusal(a domain.OwnerAsk, plan domain.BacklogPlan, itemRef string) error {
	switch {
	case a.State != domain.AskAnswered || a.Answer == nil:
		return apierr.Forbidden("PLAN_OWNER_ASK_NOT_ANSWERED", "The owner has not answered "+a.Ref+" yet")
	case a.Answer.Relayed:
		return apierr.Forbidden("PLAN_OWNER_ASK_RELAYED", a.Ref+" was answered by an orchestrator relaying a message, not by the owner: pass the owner's fresh chat message with --owner-message instead")
	case !a.AnsweredAt.After(plan.UpdatedAt):
		return apierr.Forbidden("PLAN_OWNER_ASK_STALE", "The owner answered "+a.Ref+" before the plan last changed: ask them again")
	case a.About != "" && !strings.EqualFold(a.About, itemRef):
		return apierr.Forbidden("PLAN_OWNER_ASK_OTHER_ITEM", a.Ref+" is about "+a.About+", not "+itemRef)
	}
	return nil
}
