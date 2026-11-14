//go:build !windows

package e2e

import "os"

// Every daemon these tests start inherits the process environment. Role
// defaults must not depend on whether the host has Claude Code installed.
func init() { _ = os.Setenv("TALLPINE_NO_BUILTIN_ROLE_DEFAULTS", "1") }
