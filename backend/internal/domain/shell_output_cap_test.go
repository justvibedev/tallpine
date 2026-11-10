package domain

import "testing"

func TestShellOutputLimit(t *testing.T) {
	t.Setenv(EnvShellOutputLimit, "")
	cases := []struct {
		name string
		env  map[string]string
		want int
	}{
		{"default", nil, DefaultShellOutputLimit},
		{"override", map[string]string{EnvShellOutputLimit: "20000"}, 20000},
		{"off", map[string]string{EnvShellOutputLimit: "off"}, 0},
		{"zero", map[string]string{EnvShellOutputLimit: "0"}, 0},
		{"empty project value defers to daemon", map[string]string{EnvShellOutputLimit: ""}, DefaultShellOutputLimit},
		{"garbage falls back", map[string]string{EnvShellOutputLimit: "lots"}, DefaultShellOutputLimit},
		{"negative falls back", map[string]string{EnvShellOutputLimit: "-5"}, DefaultShellOutputLimit},
	}
	for _, c := range cases {
		if got := ShellOutputLimit(c.env); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	t.Setenv(EnvShellOutputLimit, "9000")
	if got := ShellOutputLimit(nil); got != 9000 {
		t.Errorf("daemon env: got %d", got)
	}
	if got := ShellOutputLimit(map[string]string{EnvShellOutputLimit: "off"}); got != 0 {
		t.Errorf("project env must win over daemon env: got %d", got)
	}
}
