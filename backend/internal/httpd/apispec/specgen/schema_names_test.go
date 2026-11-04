package specgen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSchemaNamesHaveNoReassignedKeys fails when a schemaNames key is defined
// twice across the base map and the init() additions, where the later write
// would silently win.
func TestSchemaNamesHaveNoReassignedKeys(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		keys, err := schemaNameKeys(f, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if prev, dup := seen[k]; dup {
				t.Errorf("schema name key %s defined in %s and again in %s", k, prev, f)
			}
			seen[k] = f
		}
	}
	if len(seen) == 0 {
		t.Fatal("no schemaNames keys found")
	}
}

// TestSchemaNameKeysScanner checks the scanner counts schemaNames registrations
// only: a duplicate in an unrelated table is ignored, a real one is reported.
func TestSchemaNameKeysScanner(t *testing.T) {
	const src = `package specgen
var schemaNames = map[string]string{"A": "a", "B": "b"}
func init() {
	for k, v := range map[string]string{"C": "c"} { schemaNames[k] = v }
	schemaNames["D"] = "d"
	for _, tc := range map[string]string{"A": "x", "A2": "y"} { _ = tc }
}
`
	keys, err := schemaNameKeys("x.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keys, ","); got != "A,B,C,D" {
		t.Fatalf("keys = %s, want A,B,C,D", got)
	}
}

// schemaNameKeys lists the literal keys written into schemaNames in one file:
// the var initializer, `schemaNames[k] = v` assignments, and map literals
// ranged over by a loop that stores into schemaNames.
func schemaNameKeys(file string, src any) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
	if err != nil {
		return nil, err
	}
	var keys []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ValueSpec:
			if len(x.Names) == 1 && x.Names[0].Name == "schemaNames" && len(x.Values) == 1 {
				keys = append(keys, mapLiteralKeys(x.Values[0])...)
			}
		case *ast.RangeStmt:
			if storesIntoSchemaNames(x.Body) {
				keys = append(keys, mapLiteralKeys(x.X)...)
			}
		case *ast.AssignStmt:
			if len(x.Lhs) == 1 {
				if idx, ok := x.Lhs[0].(*ast.IndexExpr); ok && isSchemaNames(idx.X) {
					if lit, ok := idx.Index.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						k, _ := strconv.Unquote(lit.Value)
						keys = append(keys, k)
					}
				}
			}
		}
		return true
	})
	return keys, nil
}

func isSchemaNames(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "schemaNames"
}

func storesIntoSchemaNames(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok && len(a.Lhs) == 1 {
			if idx, ok := a.Lhs[0].(*ast.IndexExpr); ok && isSchemaNames(idx.X) {
				found = true
			}
		}
		return !found
	})
	return found
}

func mapLiteralKeys(e ast.Expr) []string {
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	var keys []string
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if b, ok := kv.Key.(*ast.BasicLit); ok && b.Kind == token.STRING {
				k, _ := strconv.Unquote(b.Value)
				keys = append(keys, k)
			}
		}
	}
	return keys
}
