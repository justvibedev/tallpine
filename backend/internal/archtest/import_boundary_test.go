// Package archtest holds architecture tests that read the source tree.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const adaptersPrefix = "tallpine.eu/tallpine/backend/internal/adapters"

// TestServicesAndReviewDoNotImportAdapters keeps the layering one-directional:
// adapters implement ports, services and review consume ports. A service that
// needs an adapter takes it through a port wired in daemon. Test files are
// exempt: they may build real adapters as fixtures.
func TestServicesAndReviewDoNotImportAdapters(t *testing.T) {
	for _, root := range []string{"../service", "../review"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if perr != nil {
				return perr
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				if p == adaptersPrefix || strings.HasPrefix(p, adaptersPrefix+"/") {
					t.Errorf("%s imports %s; take it through a port wired in daemon", path, p)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestNoFirstNonEmptyHelpers keeps the string-coalescing helpers from growing
// back: use cmp.Or, or textutil.FirstNonBlank/FirstTrimmed when trimming.
func TestNoFirstNonEmptyHelpers(t *testing.T) {
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && coalescerName(fn.Name.Name) {
				t.Errorf("%s declares %s; use cmp.Or or textutil.FirstNonBlank", path, fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// coalescerName matches the names these helpers have been written under.
func coalescerName(name string) bool {
	for _, prefix := range []string{"firstNonEmpty", "firstNonBlank", "firstHookValue", "firstTrimmed"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
