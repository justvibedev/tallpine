package agent

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestModelsAppliesModelGateWithoutCachingItsResult(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := successfulModelDiscoverer()
	svc := newService([]ports.HarnessAgent{harnessAgent("fx", "FX", nil)}, cache, discoverer)
	var gatedAgent string
	svc.modelGate = func(_ context.Context, agentID string, catalog ports.AgentModelCatalog) ports.AgentModelCatalog {
		gatedAgent = agentID
		models := append([]ports.AgentModelInfo(nil), catalog.Models...)
		models[0].RequiresVersion = "9.9.9"
		models[0].UnavailableReason = "update the CLI"
		catalog.Models = models
		return catalog
	}

	catalog, err := svc.Models(context.Background(), "fx", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if gatedAgent != "fx" {
		t.Fatalf("gate agent = %q, want fx", gatedAgent)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].RequiresVersion != "9.9.9" || catalog.Models[0].UnavailableReason != "update the CLI" {
		t.Fatalf("gated models = %#v", catalog.Models)
	}
	stored, ok, err := cache.GetAgentModelCatalog(context.Background(), "fx", "")
	if err != nil || !ok {
		t.Fatalf("cached catalog = (%#v, %v, %v)", stored, ok, err)
	}
	if strings.Contains(stored.CatalogJSON, "requiresVersion") {
		t.Fatalf("cache stored the gated catalog: %s", stored.CatalogJSON)
	}

	// Pickers replace their catalog with the revalidation response, so it must
	// not re-enable what the read path disabled.
	revalidated, err := svc.RevalidateModels(context.Background(), "fx", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(revalidated.Models) != 1 || revalidated.Models[0].RequiresVersion != "9.9.9" || revalidated.Models[0].UnavailableReason != "update the CLI" {
		t.Fatalf("revalidated models = %#v", revalidated.Models)
	}
	stored, ok, err = cache.GetAgentModelCatalog(context.Background(), "fx", "")
	if err != nil || !ok {
		t.Fatalf("cached catalog after revalidation = (%#v, %v, %v)", stored, ok, err)
	}
	if strings.Contains(stored.CatalogJSON, "requiresVersion") {
		t.Fatalf("revalidation cached the gated catalog: %s", stored.CatalogJSON)
	}
}
