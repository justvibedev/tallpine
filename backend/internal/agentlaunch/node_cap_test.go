package agentlaunch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func applyCaps(t *testing.T, env, project, daemon map[string]string, dir string) map[string]string {
	t.Helper()
	ApplyParallelismDefaults(env, project, fakeGetenv(daemon), 16, false, dir)
	return env
}

func TestNodeCapsAppendAndQuote(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data dir with spaces")
	env := applyCaps(t, map[string]string{}, nil, map[string]string{"NODE_OPTIONS": "--max-old-space-size=4096"}, dir)
	if env["VITEST_MAX_WORKERS"] != "4" {
		t.Fatalf("VITEST_MAX_WORKERS = %q", env["VITEST_MAX_WORKERS"])
	}
	no := env["NODE_OPTIONS"]
	if !strings.HasPrefix(no, "--max-old-space-size=4096 --require \"") || !strings.Contains(no, "data dir with spaces") {
		t.Fatalf("NODE_OPTIONS = %q", no)
	}
	// project value is appended to, and a second pass does not duplicate
	env2 := applyCaps(t, map[string]string{}, map[string]string{"NODE_OPTIONS": "--trace-warnings"}, nil, dir)
	if !strings.HasPrefix(env2["NODE_OPTIONS"], "--trace-warnings --require ") {
		t.Fatalf("NODE_OPTIONS = %q", env2["NODE_OPTIONS"])
	}
	env3 := applyCaps(t, map[string]string{"NODE_OPTIONS": env2["NODE_OPTIONS"]}, nil, nil, dir)
	if env3["NODE_OPTIONS"] != env2["NODE_OPTIONS"] {
		t.Fatalf("duplicated: %q", env3["NODE_OPTIONS"])
	}
}

func TestNodeCapsOptOutAndKeepProjectVitest(t *testing.T) {
	dir := t.TempDir()
	env := applyCaps(t, map[string]string{}, map[string]string{"TALLPINE_NODE_CAP": "off"}, nil, dir)
	if _, ok := env["NODE_OPTIONS"]; ok || env["VITEST_MAX_WORKERS"] != "" {
		t.Fatalf("opt-out leaked: %v", env)
	}
	env = applyCaps(t, map[string]string{}, map[string]string{"VITEST_MAX_WORKERS": "9"}, nil, dir)
	if env["VITEST_MAX_WORKERS"] != "9" {
		t.Fatalf("env = %v", env)
	}
	env = applyCaps(t, map[string]string{}, nil, nil, "")
	if _, ok := env["NODE_OPTIONS"]; ok {
		t.Fatalf("no dataDir must skip NODE_OPTIONS: %v", env)
	}
}

// The preload really caps what Node reports, even with spaces in the path and
// a pre-existing NODE_OPTIONS.
func TestNodeCapPreloadCapsNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := filepath.Join(t.TempDir(), "a b")
	env := applyCaps(t, map[string]string{}, nil, map[string]string{"NODE_OPTIONS": "--no-warnings"}, dir)
	cmd := exec.Command(node, "-p", "require('os').availableParallelism()+','+require('os').cpus().length")
	cmd.Env = append(os.Environ(), "NODE_OPTIONS="+env["NODE_OPTIONS"], "TALLPINE_TEST_PARALLELISM=1")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "1,1" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestNodeCapsScrubbedLaunchDoesNotInheritDaemon(t *testing.T) {
	dir := t.TempDir()
	daemon := map[string]string{"NODE_OPTIONS": "--foo", "VITEST_MAX_WORKERS": "99"}
	env := applyCaps(t, map[string]string{EnvScrub: "1"}, nil, daemon, dir)
	if strings.Contains(env["NODE_OPTIONS"], "--foo") || !strings.HasPrefix(env["NODE_OPTIONS"], "--require ") || env["VITEST_MAX_WORKERS"] != "4" {
		t.Fatalf("scrubbed env = %v", env)
	}
	env = applyCaps(t, map[string]string{EnvUnset: "NODE_OPTIONS"}, nil, daemon, dir)
	if strings.Contains(env["NODE_OPTIONS"], "--foo") {
		t.Fatalf("unset env = %v", env)
	}
}

func TestNodeCapPreloadBlocksWatchman(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "started")
	fake := "#!/bin/sh\ntouch " + marker + "\n"
	if err := os.WriteFile(filepath.Join(bin, "watchman"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	env := applyCaps(t, map[string]string{}, nil, nil, dir)
	script := `const c=require('child_process');const r=[];
r.push(c.spawnSync('watchman',['--version']).error&&'e');
r.push(c.spawnSync('/opt/homebrew/bin/watchman',['--version']).error&&'e');
try{c.execFileSync('watchman',['--version'])}catch(e){r.push('e')}
try{c.execSync('watchman --version',{stdio:'ignore'})}catch(e){r.push('e')}
const u=require('util');
u.promisify(c.execFile)('node',['-e','']).then(o=>{r.push(typeof o.stdout==='string'?'p':'x');
return u.promisify(c.exec)('watchman --version').then(()=>r.push('x'),()=>r.push('e'))}).then(()=>console.log(r.join(',')))`
	cmd := exec.Command(node, "-e", script)
	cmd.Env = append(os.Environ(), "NODE_OPTIONS="+env["NODE_OPTIONS"], "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "e,e,e,e,p,e" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("watchman was started")
	}
}
