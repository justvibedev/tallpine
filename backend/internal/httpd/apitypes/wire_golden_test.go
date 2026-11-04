package apitypes

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestWireTagsGolden pins the JSON tag of every field of every type in this
// package against testdata/wire_tags.golden. The daemon and the CLI share these
// types, so a renamed or re-flagged tag moves both sides together and no round
// trip notices; this makes it a deliberate, reviewed wire change
// (UPDATE_GOLDEN=1 go test to accept).
func TestWireTagsGolden(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, s := range gd.Specs {
				ts, ok := s.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				var tags []string
				for _, fld := range st.Fields.List {
					if fld.Tag == nil {
						continue
					}
					if j := reflect.StructTag(strings.Trim(fld.Tag.Value, "`")).Get("json"); j != "" {
						tags = append(tags, j)
					}
				}
				sort.Strings(tags)
				lines = append(lines, ts.Name.Name+": "+strings.Join(tags, " "))
			}
		}
	}
	sort.Strings(lines)
	got := []byte(strings.Join(lines, "\n") + "\n")
	path := filepath.Join("testdata", "wire_tags.golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("wire tags drifted from %s; review the diff and re-run with UPDATE_GOLDEN=1 if intended", path)
	}
}
