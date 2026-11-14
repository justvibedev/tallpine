package codex

import "runtime"

// ResolveNativeCodex unwraps a Windows npm command shim to the native codex
// executable and returns any other path unchanged. Callers that pass codex
// arguments containing double quotes (hook config) must use it: cmd.exe
// re-parses the arguments of a .cmd shim and mangles the quotes.
func ResolveNativeCodex(path string) string {
	return resolveNativeWindowsCodex(path, runtime.GOOS)
}
