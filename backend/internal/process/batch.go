package process

import "strings"

// BatchCommandLine builds the cmd.exe command line (the part between the outer
// quotes of `cmd /d /s /c "..."`) that runs a .cmd/.bat shim. cmd.exe uses
// different quoting rules from native Windows executables: each token stays
// quoted so paths and arguments with spaces survive, and embedded quotes are
// doubled so they cannot end the command early. Inside quotes ^ and & are
// literal; %VAR% expansion cannot be suppressed and is left as-is.
func BatchCommandLine(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteBatchArg(name))
	for _, arg := range args {
		parts = append(parts, quoteBatchArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteBatchArg(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
