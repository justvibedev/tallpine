package domain

import "testing"

func TestNotificationType_Urgency(t *testing.T) {
	want := map[NotificationType]NotificationUrgency{
		NotificationNeedsInput:             NotificationUrgencyCritical,
		NotificationPlanWaiting:            NotificationUrgencyCritical,
		NotificationReadyToMerge:           NotificationUrgencyAttention,
		NotificationReviewChangesRequested: NotificationUrgencyAttention,
		NotificationOrchestratorApproval:   NotificationUrgencyAttention,
		NotificationPRMerged:               NotificationUrgencyInfo,
		NotificationPRClosedUnmerged:       NotificationUrgencyInfo,
		NotificationReviewCompleted:        NotificationUrgencyInfo,
	}
	for typ, urgency := range want {
		if !typ.Valid() {
			t.Fatalf("%q is not a valid type", typ)
		}
		if got := typ.Urgency(); got != urgency {
			t.Errorf("%s: urgency %q, want %q", typ, got, urgency)
		}
	}
	if got := NotificationType("future").Urgency(); got != NotificationUrgencyInfo {
		t.Errorf("unknown type: %q", got)
	}
}
