package pricing

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
)

const (
	// ManifestMaxBytes bounds the catalog manifest.
	ManifestMaxBytes int64 = 1 << 20
	// ProviderMaxBytes bounds each provider catalog file.
	ProviderMaxBytes int64 = 8 << 20
)

// bundledFS holds the reviewed pricing catalog compiled into the binary. It is
// a byte-for-byte copy of the repo-root pricing/catalog/v1 tree; the
// TestBundledCatalogMatchesRepoCatalog test fails if the two drift. The daemon
// never fetches a catalog over the network (privacy: no third-party requests).
//
//go:embed bundled/v1
var bundledFS embed.FS

// FetchResult reports either a complete validated catalog or a valid 304.
type FetchResult struct {
	Catalog     *Catalog
	ETag        string
	NotModified bool
}

// BundledFetcher serves the catalog compiled into the binary through the same
// CatalogFetcher seam the refresher uses, so the activation, cache and backfill
// pipeline is unchanged and no network request is ever made.
type BundledFetcher struct{}

// NewBundledFetcher returns the offline catalog source used in production.
func NewBundledFetcher() *BundledFetcher { return &BundledFetcher{} }

// Fetch returns the bundled catalog, or NotModified when etag already names it.
func (BundledFetcher) Fetch(ctx context.Context, etag string, _ bool) (FetchResult, error) {
	if err := ctx.Err(); err != nil {
		return FetchResult{}, err
	}
	catalog, bundledETag, err := loadBundled()
	if err != nil {
		return FetchResult{}, err
	}
	if etag == bundledETag {
		return FetchResult{ETag: etag, NotModified: true}, nil
	}
	return FetchResult{Catalog: catalog, ETag: bundledETag}, nil
}

// LoadBundled parses and validates the catalog compiled into the binary.
func LoadBundled() (*Catalog, error) {
	catalog, _, err := loadBundled()
	return catalog, err
}

func loadBundled() (*Catalog, string, error) {
	manifestBytes, err := fs.ReadFile(bundledFS, "bundled/v1/manifest.json")
	if err != nil {
		return nil, "", fmt.Errorf("read bundled pricing manifest: %w", err)
	}
	var manifest catalogManifest
	if err := decodeStrictJSON(manifestBytes, &manifest); err != nil {
		return nil, "", fmt.Errorf("decode bundled pricing manifest: %w", err)
	}
	if err := validateRuntimeManifest(manifest); err != nil {
		return nil, "", err
	}
	providers := make(map[string][]byte, len(manifest.Providers))
	for _, ref := range manifest.Providers {
		contents, err := fs.ReadFile(bundledFS, path.Join("bundled/v1", ref.Path))
		if err != nil {
			return nil, "", fmt.Errorf("read bundled provider %q: %w", ref.ProviderID, err)
		}
		providers[ref.Path] = contents
	}
	catalog, err := ParseCatalog(manifestBytes, providers)
	if err != nil {
		return nil, "", fmt.Errorf("validate bundled pricing catalog: %w", err)
	}
	sum := sha256.Sum256(manifestBytes)
	return catalog, `"bundled-` + hex.EncodeToString(sum[:8]) + `"`, nil
}
