package rules

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

type noStore struct{}

func (noStore) AgentPolicy(context.Context) ([]byte, error)             { return nil, nil }
func (noStore) SetAgentPolicy(context.Context, []byte, time.Time) error { return nil }

// Every registered rule must have a working schema, unique name and a
// no-restriction default: the engine refuses a bad rule at startup.
func TestRegistryBuildsAnEngine(t *testing.T) {
	if _, err := agentpolicy.New(All(), noStore{}); err != nil {
		t.Fatal(err)
	}
}
