package agentlaunch

import "testing"

func fakeGetenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestApplyParallelismDefaults(t *testing.T) {
	cases := []struct {
		name            string
		env, project    map[string]string
		daemon          map[string]string
		insensitive     bool
		wantPar, wantGM string
	}{
		{name: "unset sets both", wantPar: "4", wantGM: "4"},
		{name: "project value kept", project: map[string]string{"TALLPINE_TEST_PARALLELISM": "7", "GOMAXPROCS": "9"}, wantPar: "7", wantGM: "9"},
		{name: "project parallelism feeds GOMAXPROCS", project: map[string]string{"TALLPINE_TEST_PARALLELISM": "7"}, wantPar: "7", wantGM: "7"},
		{name: "daemon value kept", daemon: map[string]string{"TALLPINE_TEST_PARALLELISM": "6", "GOMAXPROCS": "5"}, wantPar: "", wantGM: ""},
		{name: "daemon parallelism feeds GOMAXPROCS", daemon: map[string]string{"TALLPINE_TEST_PARALLELISM": "6"}, wantPar: "", wantGM: "6"},
		{name: "windows case-insensitive", project: map[string]string{"gomaxprocs": "3", "Tallpine_Test_Parallelism": "8"}, insensitive: true, wantPar: "8", wantGM: "3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			ApplyParallelismDefaults(env, tc.project, fakeGetenv(tc.daemon), 16, tc.insensitive)
			if tc.insensitive {
				// keys are copied under their canonical names or kept as the project spelled them
				if env["GOMAXPROCS"] != tc.wantGM && env["gomaxprocs"] != tc.wantGM {
					t.Fatalf("env = %v", env)
				}
				return
			}
			if env["TALLPINE_TEST_PARALLELISM"] != tc.wantPar || env["GOMAXPROCS"] != tc.wantGM {
				t.Fatalf("env = %v, want par=%q gomaxprocs=%q", env, tc.wantPar, tc.wantGM)
			}
		})
	}
}

func TestApplyParallelismDefaultsWindowsDoesNotDuplicateKeys(t *testing.T) {
	env := map[string]string{"gomaxprocs": "3"}
	ApplyParallelismDefaults(env, nil, fakeGetenv(nil), 16, true)
	if _, dup := env["GOMAXPROCS"]; dup || env["gomaxprocs"] != "3" {
		t.Fatalf("env = %v", env)
	}
}

func TestDefaultTestParallelism(t *testing.T) {
	for cores, want := range map[int]int{1: 2, 4: 2, 8: 2, 12: 3, 16: 4, 64: 16} {
		if got := DefaultTestParallelism(cores); got != want {
			t.Errorf("cores %d = %d, want %d", cores, got, want)
		}
	}
}
