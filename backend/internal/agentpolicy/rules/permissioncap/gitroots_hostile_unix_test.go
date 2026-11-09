//go:build !windows

package permissioncap

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// rootsPromptly fails rather than hanging when a hostile pointer file blocks
// the read.
func rootsPromptly(t *testing.T, repo, ws string) []string {
	t.Helper()
	done := make(chan []string, 1)
	go func() { done <- GitWritableRoots(repo, ws) }()
	select {
	case got := <-done:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("reading a hostile pointer file blocked")
		return nil
	}
}

// Both pointer files are replaceable by the agent and read by the unfenced
// daemon, so each gets every hostile shape.
func TestGitWritableRootsSurviveHostilePointerFiles(t *testing.T) {
	pointers := map[string]func(ws, private string) (file, valid string){
		".git file": func(ws, private string) (string, string) {
			return filepath.Join(ws, ".git"), "gitdir: " + private + "\n"
		},
		"private gitdir file": func(ws, private string) (string, string) {
			return filepath.Join(private, "gitdir"), filepath.Join(ws, ".git") + "\n"
		},
	}
	shapes := map[string]func(t *testing.T, file, valid string){
		"symlink to /dev/zero": func(t *testing.T, file, _ string) {
			if err := os.Symlink("/dev/zero", file); err != nil {
				t.Fatal(err)
			}
		},
		"FIFO with no writer": func(t *testing.T, file, _ string) {
			if err := syscall.Mkfifo(file, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		// Holding a valid claim, so only the regular-file check can refuse it.
		"FIFO holding a valid claim": func(t *testing.T, file, valid string) {
			if err := syscall.Mkfifo(file, 0o600); err != nil {
				t.Fatal(err)
			}
			w, err := os.OpenFile(file, os.O_RDWR|syscall.O_NONBLOCK, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = w.Close() })
			if _, err := w.WriteString(valid); err != nil {
				t.Fatal(err)
			}
		},
		// Padded so the content is still valid after trimming: only the size
		// cap can refuse it.
		"oversized file": func(t *testing.T, file, valid string) {
			writeTestFile(t, file, valid+strings.Repeat(" ", maxPointerFile))
		},
	}
	for pointer, locate := range pointers {
		for shape, plant := range shapes {
			t.Run(pointer+"/"+shape, func(t *testing.T) {
				_, repo, _, private, ws := linkedLayout(t)
				file, valid := locate(ws, private)
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				plant(t, file, valid)
				if got := rootsPromptly(t, repo, ws); got != nil {
					t.Fatalf("a hostile pointer file produced roots: %v", got)
				}
			})
		}
	}
}
