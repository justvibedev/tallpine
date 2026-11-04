package processenv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeWindowsPATHExecutesBundledTallpine(t *testing.T) {
	const modeKey = "TALLPINE_TEST_WINDOWS_PATH_MODE"
	switch os.Getenv(modeKey) {
	case "identity":
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("TALLPINE_IDENTITY=" + exe)
		return
	case "merge":
		bundle := os.Getenv("TALLPINE_TEST_BUNDLE_DIR")
		cmd := exec.CommandContext(context.Background(), os.Getenv("ComSpec"), "/d", "/c", "tallpine.exe", "-test.run=^TestMergeWindowsPATHExecutesBundledTallpine$")
		cmd.Dir = t.TempDir()
		cmd.Env = Merge(map[string]string{
			"PATH":  bundle + ";" + os.Getenv("Path"),
			"Path":  os.Getenv("TALLPINE_TEST_FOREIGN_DIR") + ";" + os.Getenv("Path"),
			modeKey: "identity",
		})
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(strings.ToLower(string(output)), strings.ToLower("TALLPINE_IDENTITY="+filepath.Join(bundle, "tallpine.exe"))) {
			t.Fatalf("bundled Tallpine identity: %v\n%s", err, output)
		}
		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	bundle, foreign := t.TempDir(), t.TempDir()
	for _, dir := range []string{bundle, foreign} {
		if err := os.WriteFile(filepath.Join(dir, "tallpine.exe"), binary, 0o700); err != nil { //nolint:gosec // executable test fixture
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(context.Background(), exe, "-test.run=^TestMergeWindowsPATHExecutesBundledTallpine$")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "PATH") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "Path="+foreign+";"+os.Getenv("PATH"), modeKey+"=merge", "TALLPINE_TEST_BUNDLE_DIR="+bundle, "TALLPINE_TEST_FOREIGN_DIR="+foreign)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mixed-case environment process: %v\n%s", err, output)
	}
}
