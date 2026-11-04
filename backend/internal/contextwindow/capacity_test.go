package contextwindow

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func catalog(models ...ports.AgentModelInfo) ports.AgentModelCatalog {
	return ports.AgentModelCatalog{Models: models}
}

func TestCapacityOrder(t *testing.T) {
	windowed := catalog(
		ports.AgentModelInfo{ID: "claude-opus-5-5", ContextWindow: 500_000},
		ports.AgentModelInfo{ID: "claude-opus-4-1", ContextWindow: 200_000},
		ports.AgentModelInfo{ID: "exact", ContextWindow: 123_000},
	)
	cases := []struct {
		name    string
		catalog ports.AgentModelCatalog
		model   string
		want    int64
	}{
		{"exact id wins", windowed, "exact", 123_000},
		{"alias resolves to the newest windowed family model", windowed, "opus", 500_000},
		{"alias with context suffix", windowed, "opus[1m]", 500_000},
		{"alias with no catalog window uses the family table", catalog(), "opus", 1_000_000},
		{"haiku family table", catalog(), "haiku", 200_000},
		{"full sonnet id uses the family table", catalog(), "claude-sonnet-5-5", 1_000_000},
		{"full opus id uses the family table", catalog(), "claude-opus-5-5", 1_000_000},
		{"full haiku id uses the family table", catalog(), "claude-haiku-5-5", 200_000},
		{"full fable id uses the family table", catalog(), "claude-fable-5-1", 1_000_000},
		{"full id with context suffix", catalog(), "claude-sonnet-5-5[1m]", 1_000_000},
		{"non-Claude id naming a family stays unknown", catalog(), "gpt-sonnet-5", 0},
		{"unknown model is unknown", catalog(), "custom-model", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Capacity(tc.catalog, tc.model); got != tc.want {
				t.Fatalf("Capacity(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

func TestLabel(t *testing.T) {
	c := catalog(ports.AgentModelInfo{ID: "opus", Label: "Opus 5.5"}, ports.AgentModelInfo{ID: "raw", Label: "raw"})
	if got := Label(c, "opus"); got != "Opus 5.5" {
		t.Fatalf("catalog label = %q", got)
	}
	if got := Label(catalog(), "sonnet"); got != "Sonnet" {
		t.Fatalf("family label = %q", got)
	}
	if got := Label(c, "raw"); got != "" {
		t.Fatalf("label equal to id = %q, want empty", got)
	}
}

type fakeReader struct {
	catalog ports.AgentModelCatalog
	err     error
}

func (f fakeReader) Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error) {
	return f.catalog, f.err
}

func TestLookup(t *testing.T) {
	ctx := context.Background()
	c := catalog(ports.AgentModelInfo{ID: "exact", Label: "Exact One", ContextWindow: 123_000})
	if got, label := Lookup(ctx, fakeReader{catalog: c}, "claude-code", "p", "exact"); got != 123_000 || label != "Exact One" {
		t.Fatalf("Lookup = %d %q", got, label)
	}
	if got, label := Lookup(ctx, nil, "claude-code", "p", "custom"); got != 0 || label != "" {
		t.Fatalf("nil reader, unknown model = %d %q, want 0 and empty", got, label)
	}
	if got, label := Lookup(ctx, fakeReader{err: errors.New("boom")}, "claude-code", "p", "custom"); got != 0 || label != "" {
		t.Fatalf("failing reader, unknown model = %d %q, want 0 and empty", got, label)
	}
}

func TestLookupFallsBackToFamilyTable(t *testing.T) {
	cases := map[string]CatalogReader{
		"nil reader":      nil,
		"erroring reader": fakeReader{err: errors.New("no catalog")},
	}
	for name, reader := range cases {
		window, label := Lookup(context.Background(), reader, "claude-code", "p", "opus")
		if window != 1_000_000 || label != "Opus" {
			t.Errorf("%s: got %d %q, want 1000000 Opus", name, window, label)
		}
	}
}

func TestLookupFullIDForPercentThreshold(t *testing.T) {
	for _, reader := range []CatalogReader{nil, fakeReader{err: errors.New("no catalog")}} {
		window, _ := Lookup(context.Background(), reader, "claude-code", "p", "claude-sonnet-5-5")
		if got := window * 40 / 100; got != 400_000 {
			t.Fatalf("40%% of claude-sonnet-5-5 = %d, want 400000", got)
		}
	}
}
