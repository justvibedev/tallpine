package httpd

import (
	"strings"
	"testing"
)

// planAutoApprove is not in the creating orchestrator's allowlist: neither a
// project config body nor a created project's config may carry it.
func TestAgentConfigViolationRefusesPlanAutoApprove(t *testing.T) {
	if why := agentConfigViolation([]byte(`{"planAutoApprove":true}`)); !strings.Contains(why, "planAutoApprove") {
		t.Fatalf("violation = %q, want it to name planAutoApprove", why)
	}
	if why := configBodyViolation([]byte(`{"config":{"noCI":true,"planAutoApprove":true}}`)); !strings.Contains(why, "planAutoApprove") {
		t.Fatalf("config body violation = %q", why)
	}
	// Off is the same as absent, as for every field.
	if why := agentConfigViolation([]byte(`{"planAutoApprove":false}`)); why != "" {
		t.Fatalf("false = %q, want accepted as empty", why)
	}
}
