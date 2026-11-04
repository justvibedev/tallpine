// Command gencodexproto generates Go bindings for the Codex app-server protocol
// from the schema the provider itself publishes.
//
// The protocol is a machine-readable contract: `codex app-server
// generate-json-schema --out DIR` writes the whole method and payload surface as
// draft-07 JSON Schema. Reading it beats hand-writing structs, because the
// failure mode of hand-writing is silent: a field spelled slightly wrong
// unmarshals to a zero value and the driver reports "no output" rather than
// "wrong shape". Every payload here is a compile-time type instead.
//
// Usage:
//
//	go run ./cmd/gencodexproto -out internal/adapters/chatdriver/codexappserver/codexproto/protocol.gen.go
//
// With no -schema, the generator asks the installed provider for its schema, so
// what lands in the tree always describes a real build rather than a snapshot
// someone remembered to update.
package main

import (
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	var (
		schemaDir = flag.String("schema", "", "directory of JSON Schema files; empty asks the installed provider")
		out       = flag.String("out", "", "path of the Go file to write (required)")
		binary    = flag.String("codex", "codex", "provider binary used when -schema is empty")
	)
	flag.Parse()

	if *out == "" {
		fatal("-out is required")
	}

	dir := *schemaDir
	version := "unknown"
	if dir == "" {
		tmp, err := os.MkdirTemp("", "codexschema")
		if err != nil {
			fatal("temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		if err := emitSchema(*binary, tmp); err != nil {
			fatal("%v", err)
		}
		dir = tmp
	}
	if v, err := providerVersion(*binary); err == nil {
		version = v
	}

	g, err := load(dir)
	if err != nil {
		fatal("%v", err)
	}
	g.version = version

	if err := os.MkdirAll(filepath.Dir(*out), 0o750); err != nil {
		fatal("mkdir: %v", err)
	}

	src := g.render()
	formatted, err := format.Source(src)
	if err != nil {
		// Write the unformatted source next to the target so the syntax error is
		// readable; a gofmt failure here is a generator bug, not a schema problem.
		_ = os.WriteFile(*out+".broken", src, 0o600)
		fatal("gofmt: %v (unformatted source at %s.broken)", err, *out)
	}
	if err := os.WriteFile(*out, formatted, 0o600); err != nil {
		fatal("write: %v", err)
	}
	fmt.Fprintf(os.Stderr, "gencodexproto: %d types, %d methods from %s\n",
		len(g.defs), len(g.methods), version)
}

func fatal(formatText string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "gencodexproto: "+formatText+"\n", args...)
	os.Exit(1)
}

func emitSchema(binary, dir string) error {
	cmd := exec.Command(binary, "app-server", "generate-json-schema", "--out", dir) //nolint:forbidigo // code generation runs outside the daemon
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s app-server generate-json-schema: %w: %s", binary, err, out)
	}
	return nil
}

func providerVersion(binary string) (string, error) {
	out, err := exec.Command(binary, "--version").Output() //nolint:forbidigo // code generation runs outside the daemon
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

/* ---- schema model ------------------------------------------------------- */
