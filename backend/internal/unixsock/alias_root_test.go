package unixsock

import "testing"

// B463: a sandboxed shell points aliases at the one /tmp dir it may write.
func TestShortAliasRootHonorsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TALLPINE_SOCKET_ALIAS_ROOT", dir)
	if got := ShortAliasRoot(); got != dir {
		t.Fatalf("ShortAliasRoot = %q, want %q", got, dir)
	}
	t.Setenv("TALLPINE_SOCKET_ALIAS_ROOT", dir+"/missing")
	if got := ShortAliasRoot(); got == dir+"/missing" {
		t.Fatalf("a missing override dir must be ignored, got %q", got)
	}
}
