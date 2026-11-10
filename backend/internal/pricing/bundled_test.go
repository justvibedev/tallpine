package pricing

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestBundledFetcherServesCatalogOfflineAndHonorsETag(t *testing.T) {
	fetcher := NewBundledFetcher()
	first, err := fetcher.Fetch(t.Context(), "", false)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if first.NotModified || first.Catalog == nil || first.ETag == "" {
		t.Fatalf("first fetch = %+v, want a complete catalog with an ETag", first)
	}
	second, err := fetcher.Fetch(t.Context(), first.ETag, true)
	if err != nil {
		t.Fatalf("Fetch with ETag: %v", err)
	}
	if !second.NotModified || second.Catalog != nil {
		t.Fatalf("second fetch = %+v, want NotModified", second)
	}
	for _, providerID := range []string{"anthropic", "openai", "zai"} {
		if first.Catalog.Snapshot().ProviderVersion(providerID) == "" {
			t.Errorf("bundled catalog has no version for provider %q", providerID)
		}
	}
}

func TestBundledCatalogMatchesRepoCatalog(t *testing.T) {
	repoCatalog := filepath.Join("..", "..", "..", "pricing", "catalog", "v1")
	if _, err := os.Stat(repoCatalog); err != nil {
		t.Skipf("repo pricing catalog not available: %v", err)
	}
	seen := 0
	err := fs.WalkDir(bundledFS, "bundled/v1", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		seen++
		embedded, err := fs.ReadFile(bundledFS, name)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel("bundled/v1", filepath.FromSlash(name))
		if err != nil {
			return err
		}
		want, err := os.ReadFile(filepath.Join(repoCatalog, relative))
		if err != nil {
			t.Errorf("bundled file %s has no counterpart in pricing/catalog/v1: %v", relative, err)
			return nil
		}
		if !bytes.Equal(embedded, want) {
			t.Errorf("bundled file %s differs from pricing/catalog/v1; re-copy the catalog after regenerating it", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	repoFiles := 0
	_ = filepath.WalkDir(repoCatalog, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			repoFiles++
		}
		return nil
	})
	if seen != repoFiles {
		t.Errorf("bundled catalog has %d files, pricing/catalog/v1 has %d; re-copy the catalog", seen, repoFiles)
	}
}

func TestBundledCatalogPricesFable52(t *testing.T) {
	fetched, err := NewBundledFetcher().Fetch(t.Context(), "", false)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := fetched.Catalog.Snapshot().ProviderForModel("claude-fable-5-2"); got != "anthropic" {
		t.Fatalf("ProviderForModel(claude-fable-5-2) = %q, want anthropic", got)
	}
}
