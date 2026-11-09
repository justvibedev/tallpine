package config

import "testing"

func TestHardenRootFindsCanonicalLayoutDespiteDataDirOverride(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"packaged app sets the data dir", Config{DataDir: "/h/.tallpine/data", RunFilePath: "/h/.tallpine/running.json", StateDir: "/h/.tallpine/data"}, "/h/.tallpine"},
		{"default", Config{DataDir: "/h/.tallpine/data", RunFilePath: "/h/.tallpine/running.json", StateDir: "/h/.tallpine"}, "/h/.tallpine"},
		{"run file elsewhere keeps StateDir", Config{DataDir: "/srv/data", RunFilePath: "/tmp/running.json", StateDir: "/srv/data"}, "/srv/data"},
		{"data dir not named data keeps StateDir", Config{DataDir: "/x/state", RunFilePath: "/x/running.json", StateDir: "/x/state"}, "/x/state"},
		{"no run file keeps StateDir", Config{DataDir: "/x/data", StateDir: "/x/data"}, "/x/data"},
	} {
		if got := tc.cfg.HardenRoot(); got != tc.want {
			t.Errorf("%s: HardenRoot = %q, want %q", tc.name, got, tc.want)
		}
	}
}
