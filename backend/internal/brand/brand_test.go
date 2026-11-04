package brand

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityDoesNotCollideWithOfficialInstall(t *testing.T) {
	if CLIName == "ao" || HomeDirName == ".ao" || DefaultPort == 3001 || TmuxSocketName == "ao" {
		t.Fatalf("brand collides with the official AO install: %q %q %d %q", CLIName, HomeDirName, DefaultPort, TmuxSocketName)
	}
	if strings.Contains(MobileWakeLabel, "aoagents") || !strings.HasPrefix(MobileWakeLabel, "ee.justdev.tallpine.") {
		t.Fatalf("unexpected wake label %q", MobileWakeLabel)
	}
	if BranchPrefix == "ao" || BranchPrefix == LegacyBranchPrefix {
		t.Fatalf("generated branch prefix %q collides with the official install", BranchPrefix)
	}
	if SessionLabel == "ao.session" || SpareLabel == "ao.spare" {
		t.Fatalf("docker reaper labels collide with the official install: %q %q", SessionLabel, SpareLabel)
	}
	if got := Env("DATA_DIR"); got != "TALLPINE_DATA_DIR" {
		t.Fatalf("Env(DATA_DIR) = %q", got)
	}
}

// The Tallpine identity, pinned so an accidental edit shows up here and in
// frontend/src/shared/brand.test.ts.
func TestTallpineIdentityValues(t *testing.T) {
	for name, pair := range map[string][2]string{
		"ProductName": {ProductName, "Tallpine"}, "CLIName": {CLIName, "tallpine"}, "CLIAlias": {CLIAlias, "tp"},
		"BundleID": {BundleID, "ee.justdev.tallpine"}, "HomeDirName": {HomeDirName, ".tallpine"},
		"EnvPrefix": {EnvPrefix, "TALLPINE_"}, "TmuxSocketName": {TmuxSocketName, "tallpine"},
		"LaunchdLabelPrefix": {LaunchdLabelPrefix, "ee.justdev.tallpine"}, "URLScheme": {URLScheme, "tallpine"},
		"InAppLinkScheme": {InAppLinkScheme, "tallpine"}, "SkillName": {SkillName, "using-tallpine"},
		"MCPServerName": {MCPServerName, "tallpine"}, "SessionLabel": {SessionLabel, "tallpine.session"},
		"SpareLabel": {SpareLabel, "tallpine.spare"}, "BranchPrefix": {BranchPrefix, "tp"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
		}
	}
	if DefaultPort != 3101 {
		t.Errorf("DefaultPort = %d, want 3101", DefaultPort)
	}
	if got := BranchPrefixes(); len(got) != 2 || got[0] != "tp" || got[1] != "vo" {
		t.Errorf("BranchPrefixes() = %v, want [tp vo]", got)
	}
	pairs := LabelPairs()
	if len(pairs) != 2 || pairs[0].Session != "tallpine.session" || pairs[1].Session != "vo.session" || pairs[1].Spare != "vo.spare" {
		t.Errorf("LabelPairs() = %v, want tallpine then vo", pairs)
	}
}

func TestHomeDir(t *testing.T) {
	t.Setenv("HOME", "/tmp/brand-home")
	got, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/tmp/brand-home", ".tallpine"); got != want {
		t.Fatalf("HomeDir() = %q, want %q", got, want)
	}
}

func TestLookupEnvReadsOnlyTallpinePrefix(t *testing.T) {
	t.Setenv("TALLPINE_BRAND_PROBE", "new")
	if got := Getenv("BRAND_PROBE"); got != "new" {
		t.Fatalf("got %q, want new", got)
	}
	t.Setenv("TALLPINE_BRAND_PROBE", "")
	if got, ok := LookupEnv("BRAND_PROBE"); !ok || got != "" {
		t.Fatalf("empty value still counts as set: got %q, %v", got, ok)
	}
	os.Unsetenv("TALLPINE_BRAND_PROBE")
	if _, ok := LookupEnv("BRAND_PROBE"); ok {
		t.Fatal("unset variable reported as set")
	}
	t.Setenv("VO_BRAND_PROBE", "old")
	if _, ok := LookupEnv("BRAND_PROBE"); ok {
		t.Fatal("the retired VO_ prefix must not be read")
	}
}

func TestHookPrefixesAcceptBothCLINames(t *testing.T) {
	for _, tc := range []struct{ prefix, command string }{
		{"vo hooks codex ", "tallpine hooks codex stop"},
		{"vo hooks codex ", "vo hooks codex stop"},
		{"tallpine hooks codex ", "vo hooks codex stop"},
	} {
		if !IsHookCommand(tc.command, tc.prefix) {
			t.Errorf("IsHookCommand(%q, %q) = false", tc.command, tc.prefix)
		}
	}
	if IsHookCommand("ao hooks codex stop", "vo hooks codex ") || IsHookCommand("tallpine hooks kiro stop", "vo hooks codex ") {
		t.Error("official ao hooks and other agents must not match")
	}
	if got := HookPrefixes("vo hooks codex "); len(got) != 2 || got[1] != "tallpine hooks codex " {
		t.Errorf("HookPrefixes = %v", got)
	}
}
