package daemon

import "testing"

func TestParseDiffLines(t *testing.T) {
	d := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@ func planWidget() {\n-old\n+new\ndiff --git a/n.go b/n.go\nnew file mode 100644\n--- /dev/null\n+++ b/n.go\n@@ -0,0 +1 @@\n+z\n"
	got := parseDiffLines(d)
	if len(got["x.go"]) != 3 || got["x.go"][0] != " func planWidget() {" {
		t.Fatalf("x.go = %v", got["x.go"])
	}
	if _, ok := got["n.go"]; ok {
		t.Fatal("added file must have no entry")
	}
}
