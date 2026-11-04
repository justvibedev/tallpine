package cli

// dto_mirror_guard_test.go keeps the CLI free of hand-mirrored wire DTOs. A
// CLI-local struct whose JSON keys are all carried by one shared wire type
// (httpd/apitypes or domain) is a copy that can drift from the daemon; use the
// shared type instead.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// jsonKeys returns the JSON keys of every struct declared in the .go files
// (non-test) of dir.
func jsonKeys(t *testing.T, dir string) map[string]map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]bool{}
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
				st, isStruct := ts.Type.(*ast.StructType)
				if !isStruct {
					continue
				}
				keys := map[string]bool{}
				for _, fld := range st.Fields.List {
					if fld.Tag == nil {
						continue
					}
					tag := reflect.StructTag(strings.Trim(fld.Tag.Value, "`")).Get("json")
					if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
						keys[name] = true
					}
				}
				out[ts.Name.Name+"@"+filepath.Base(f)] = keys
			}
		}
	}
	return out
}

// mirrorAllowlist names CLI structs that intentionally keep their own shape
// (not a copy of a daemon DTO); each needs a reason.
var mirrorAllowlist = map[string]string{
	// The project-config family keeps pointer and omitempty presence semantics
	// that domain.ProjectConfig lacks: a CLI edit of one field must not emit
	// containerReap/chatHibernation/reviewers it never read (set-config merges
	// the daemon's JSON, see project_set_config_merge_test.go).
	"agentConfig":         "project-config presence semantics",
	"roleOverride":        "project-config presence semantics",
	"trackerIntakeConfig": "project-config presence semantics",
	"reviewerConfig":      "project-config presence semantics",
	"projectConfig":       "project-config presence semantics",
	// sessionTopEntry embeds apitypes.SessionView and adds only the two memory
	// columns the CLI measures itself.
	"sessionTopEntry": "SessionView plus local memory readings",
}

func TestCLIHasNoHandMirroredDTOs(t *testing.T) {
	cli := jsonKeys(t, ".")
	var shared []map[string]bool
	for _, dir := range []string{"../httpd/apitypes", "../domain"} {
		for _, k := range jsonKeys(t, dir) {
			shared = append(shared, k)
		}
	}
	var offenders []string
	for id, keys := range cli {
		name, _, _ := strings.Cut(id, "@")
		if len(keys) < 2 || mirrorAllowlist[name] != "" {
			continue
		}
		for _, sk := range shared {
			if subset(keys, sk) {
				offenders = append(offenders, id)
				break
			}
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("CLI structs duplicate a shared wire type's JSON keys; use httpd/apitypes or domain instead:\n  %s", strings.Join(offenders, "\n  "))
	}
}

func subset(a, b map[string]bool) bool {
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
