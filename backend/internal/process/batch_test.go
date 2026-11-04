package process

import "testing"

func TestBatchCommandLine(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		args []string
		want string
	}{
		{"no args", `C:\a\x.cmd`, nil, `"C:\a\x.cmd"`},
		{"spaces", `C:\Program Files\x.cmd`, []string{"a b"}, `"C:\Program Files\x.cmd" "a b"`},
		{"quotes doubled", "x.cmd", []string{`say "hi"`}, `"x.cmd" "say ""hi"""`},
		{"percent kept", "x.cmd", []string{"%PATH%"}, `"x.cmd" "%PATH%"`},
		{"caret and amp literal in quotes", "x.cmd", []string{"a^b", "c&d"}, `"x.cmd" "a^b" "c&d"`},
		{"empty arg", "x.cmd", []string{""}, `"x.cmd" ""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BatchCommandLine(tt.exe, tt.args); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
