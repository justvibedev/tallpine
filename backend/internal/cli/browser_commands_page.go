package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Limits shared with the desktop (frontend/src/main/browser-view-host/agent-action-page-cdp.ts).
const (
	maxBrowserEvalExpression = 100_000
	maxBrowserEvalTimeout    = 30_000
	maxBrowserUploadFiles    = 32
)

// addBrowserPageCommands registers history navigation, eval and upload (B857).
func addBrowserPageCommands(cmd *cobra.Command, ctx *commandContext, jsonOutput *bool) {
	for _, action := range []struct{ name, short string }{
		{name: "back", short: "Go back one page in the session's browser tab"},
		{name: "forward", short: "Go forward one page in the session's browser tab"},
		{name: "reload", short: "Reload the page in the session's browser tab"},
	} {
		cmd.AddCommand(&cobra.Command{
			Use:   action.name,
			Short: action.short,
			Long: action.short + ".\n\nWaits up to 10 seconds for the page to commit, then prints its URL;\n" +
				"run `tallpine browser wait --load` before reading a slow page.",
			Args: noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return ctx.runBrowserAction(cmd, action.name, map[string]any{}, *jsonOutput)
			},
		})
	}

	var evalTimeout int
	evalCmd := &cobra.Command{
		Use:   "eval <expression|->",
		Short: "Evaluate a JavaScript expression in the page and print its JSON value",
		Long: "Evaluate a JavaScript expression in the page's main frame and print its value as JSON.\n" +
			"Pass - to read the expression from stdin. A returned promise is awaited.\n\n" +
			"The result must be JSON-serializable (DOM nodes print as {}); results over 64 KiB\n" +
			"are cut. The script is stopped at --timeout. eval runs only in agent tabs, or in\n" +
			"the person's own tabs once they granted the agent their logins.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			expression := args[0]
			if expression == "-" {
				data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxBrowserEvalExpression+1))
				if err != nil {
					return err
				}
				expression = string(data)
			}
			if strings.TrimSpace(expression) == "" {
				return usageError{errors.New("expression is required")}
			}
			if len(expression) > maxBrowserEvalExpression {
				return usageError{fmt.Errorf("expression must be at most %d characters", maxBrowserEvalExpression)}
			}
			if evalTimeout < 1 || evalTimeout > maxBrowserEvalTimeout {
				return usageError{fmt.Errorf("--timeout must be between 1 and %d milliseconds", maxBrowserEvalTimeout)}
			}
			return ctx.runBrowserAction(cmd, "eval", map[string]any{"expression": expression, "timeoutMs": evalTimeout}, *jsonOutput)
		},
	}
	evalCmd.Flags().IntVar(&evalTimeout, "timeout", 5_000, "stop the script after this many milliseconds")
	cmd.AddCommand(evalCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "upload <ref> <path>...",
		Short: "Choose files for a file input or upload button without opening a dialog",
		Long: "Click an element reference from the latest snapshot (a file input, or a button that\n" +
			"opens one) with the file chooser intercepted, and choose the given files. No dialog\n" +
			"opens on the person's screen.\n\n" +
			"Files must be inside this session's worktree or its $TMPDIR; symlinks are resolved\n" +
			"before that check.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return usageError{errors.New("upload requires a ref and at least one path")}
			}
			if len(args)-1 > maxBrowserUploadFiles {
				return usageError{fmt.Errorf("upload accepts at most %d files", maxBrowserUploadFiles)}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := make([]any, 0, len(args)-1)
			for _, path := range args[1:] {
				absolute, err := filepath.Abs(path)
				if err != nil {
					return usageError{fmt.Errorf("resolve %s: %w", path, err)}
				}
				paths = append(paths, absolute)
			}
			return ctx.runBrowserAction(cmd, "upload", map[string]any{"ref": args[0], "paths": paths}, *jsonOutput)
		},
	})
}

// writeBrowserPageResult prints eval and upload results; ok is false for the
// other actions, which the generic writer prints.
func writeBrowserPageResult(cmd *cobra.Command, action string, result map[string]any) (bool, error) {
	switch action {
	case "eval":
		text, _ := result["json"].(string)
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), browserUntrustedText(text)); err != nil {
			return true, err
		}
		if truncated, _ := result["truncated"].(bool); truncated {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Result cut to %d of %s characters.\n", len(text), numberString(result["length"]))
			return true, err
		}
		return true, nil
	case "upload":
		files, _ := result["files"].([]any)
		names := make([]string, 0, len(files))
		for _, file := range files {
			if name, ok := file.(string); ok {
				names = append(names, name)
			}
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "Uploaded %d file(s): %s\n", len(names), strings.Join(names, ", "))
		return true, err
	}
	return false, nil
}
