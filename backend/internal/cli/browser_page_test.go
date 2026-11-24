package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserHistoryCommands(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	capture := &browserRequestCapture{result: `{"url":"https://a.example/one","title":"One","isLoading":false}`}
	srv := browserCLIServer(t, capture)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	for _, action := range []string{"back", "forward", "reload"} {
		out, _, err := executeCLI(t, deps, "browser", action)
		if err != nil {
			t.Fatal(err)
		}
		if capture.body.Action != action || len(capture.body.Args) != 0 {
			t.Fatalf("%s sent %#v", action, capture.body)
		}
		if out != browserUntrustedText("https://a.example/one")+"\n" {
			t.Fatalf("%s output = %q", action, out)
		}
	}
	if _, _, err := executeCLI(t, deps, "browser", "back", "extra"); err == nil {
		t.Fatal("back accepted an argument")
	}
}

func TestBrowserEvalSendsExpressionAndPrintsUntrustedJSON(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	capture := &browserRequestCapture{result: `{"type":"object","json":"{\"a\":1}","value":{"a":1}}`}
	srv := browserCLIServer(t, capture)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, _, err := executeCLI(t, deps, "browser", "eval", "({a: 1})", "--timeout", "250")
	if err != nil {
		t.Fatal(err)
	}
	if capture.body.Action != "eval" || capture.body.Args["expression"] != "({a: 1})" || capture.body.Args["timeoutMs"] != float64(250) {
		t.Fatalf("eval sent %#v", capture.body)
	}
	if out != browserUntrustedText(`{"a":1}`)+"\n" {
		t.Fatalf("eval output = %q", out)
	}

	deps.In = strings.NewReader("document.title\n")
	if _, _, err := executeCLI(t, deps, "browser", "eval", "-"); err != nil {
		t.Fatal(err)
	}
	if capture.body.Args["expression"] != "document.title\n" || capture.body.Args["timeoutMs"] != float64(5000) {
		t.Fatalf("eval from stdin sent %#v", capture.body.Args)
	}

	capture.result = `{"type":"string","json":"\"xx","truncated":true,"length":70002}`
	out, _, err = executeCLI(t, deps, "browser", "eval", "big")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "Result cut to 3 of 70002 characters.\n") {
		t.Fatalf("truncated output = %q", out)
	}

	capture.body.Action = ""
	for _, args := range [][]string{{"eval", "1", "--timeout", "0"}, {"eval", "1", "--timeout", "30001"}, {"eval", "  "}, {"eval", strings.Repeat("x", 100_001)}} {
		if _, _, err := executeCLI(t, deps, append([]string{"browser"}, args...)...); err == nil {
			t.Fatalf("%v accepted", args[1:])
		}
	}
	if capture.body.Action != "" {
		t.Fatalf("an invalid eval reached the daemon: %#v", capture.body)
	}
}

func TestBrowserUploadSendsAbsolutePaths(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	capture := &browserRequestCapture{result: `{"uploaded":2,"files":["a.txt","b.png"],"multiple":true}`}
	srv := browserCLIServer(t, capture)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, _, err := executeCLI(t, deps, "browser", "upload", "e3", "a.txt", "/abs/b.png")
	if err != nil {
		t.Fatal(err)
	}
	relative, _ := filepath.Abs("a.txt")
	paths, _ := capture.body.Args["paths"].([]any)
	if capture.body.Action != "upload" || capture.body.Args["ref"] != "e3" || len(paths) != 2 || paths[0] != relative || paths[1] != "/abs/b.png" {
		t.Fatalf("upload sent %#v", capture.body)
	}
	if out != "Uploaded 2 file(s): a.txt, b.png\n" {
		t.Fatalf("upload output = %q", out)
	}
	if _, _, err := executeCLI(t, deps, "browser", "upload", "e3"); err == nil {
		t.Fatal("upload without a path was accepted")
	}
}
