package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// addBrowserCoreCommands registers the core subcommands of `tallpine browser`.
func addBrowserCoreCommands(cmd *cobra.Command, ctx *commandContext, jsonOutput *bool) {
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show whether the desktop browser runtime is connected",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := ctx.browserStatus(cmd.Context())
			if err != nil {
				return err
			}
			if *jsonOutput {
				return writeJSON(cmd.OutOrStdout(), status)
			}
			state := "disconnected"
			if status.Connected {
				state = "connected"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Browser runtime: %s (%s)\n", state, status.Transport)
			return err
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "open <url>",
		Short: "Open a URL in this session's browser",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runBrowserAction(cmd, "open", map[string]any{"url": args[0]}, *jsonOutput)
		},
	})

	var interactiveOnly, snapshotDelta, snapshotFull bool
	snapshot := &cobra.Command{
		Use:   "snapshot",
		Short: "Print a compact accessibility snapshot with actionable element refs",
		Long: "Print a compact accessibility snapshot with actionable element refs.\n\n" +
			"With --delta, the first call returns the full tree and later calls return only\n" +
			"\"unchanged\" or the changes since the snapshot you last received. Use --delta --full\n" +
			"to get the full tree again.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if snapshotFull && !snapshotDelta {
				return usageError{errors.New("--full requires --delta")}
			}
			args := map[string]any{"interactive": interactiveOnly}
			if snapshotDelta {
				args["delta"] = true
			}
			if snapshotFull {
				args["full"] = true
			}
			return ctx.runBrowserAction(cmd, "snapshot", args, *jsonOutput)
		},
	}
	snapshot.Flags().BoolVar(&interactiveOnly, "interactive", false, "include only actionable elements")
	snapshot.Flags().BoolVar(&snapshotDelta, "delta", false, "return the full tree once, then only unchanged or the changes since your last snapshot")
	snapshot.Flags().BoolVar(&snapshotFull, "full", false, "with --delta, return the full tree and reset the delta baseline")
	cmd.AddCommand(snapshot)

	var actVerb, actValue, actExpectURL, actExpectText string
	var actNth int
	var actNthSet, actExpectDialog, actExpectNavigation, actExpectDOMChange bool
	var actPostconditionTimeout int
	act := &cobra.Command{
		Use:   "act <instruction>",
		Short: "Resolve an element by description and act on it, snapshotting and retrying automatically",
		Long: "Snapshots, finds the best-matching element for <instruction> against role/name/text\n" +
			"(no LLM call — deterministic matching, same as reading a snapshot yourself), and performs\n" +
			"--action on it (default click). Retries once on a stale reference. If the match is\n" +
			"ambiguous or absent, returns the candidates or snapshot instead of guessing — fall back\n" +
			"to a manual snapshot/click, retry with --nth, or refine the instruction.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			actArgs := map[string]any{"instruction": args[0], "action": actVerb}
			if cmd.Flags().Changed("value") {
				actArgs["value"] = actValue
			}
			if actNthSet {
				actArgs["nth"] = actNth
			}
			var postcondition map[string]any
			switch {
			case actExpectURL != "":
				postcondition = map[string]any{"kind": "url", "value": actExpectURL}
			case actExpectText != "":
				postcondition = map[string]any{"kind": "text", "value": actExpectText}
			case actExpectDialog:
				postcondition = map[string]any{"kind": "dialog"}
			case actExpectNavigation:
				postcondition = map[string]any{"kind": "navigation"}
			case actExpectDOMChange:
				postcondition = map[string]any{"kind": "dom-change"}
			}
			if postcondition != nil {
				postcondition["timeoutMs"] = actPostconditionTimeout
				actArgs["postcondition"] = postcondition
			}
			return ctx.runBrowserAction(cmd, "act", actArgs, *jsonOutput)
		},
	}
	act.Flags().StringVar(&actVerb, "action", "click", "verb to perform on the matched element (click, dblclick, focus, hover, fill, type, check, uncheck)")
	act.Flags().StringVar(&actValue, "value", "", "text to fill/type; required when --action is fill or type")
	act.Flags().IntVar(&actNth, "nth", 0, "0-based index to disambiguate when multiple candidates match equally")
	act.Flags().StringVar(&actExpectURL, "expect-url", "", "wait for a URL containing this value after dispatch")
	act.Flags().StringVar(&actExpectText, "expect-text", "", "wait for this page text after dispatch")
	act.Flags().BoolVar(&actExpectDialog, "expect-dialog", false, "wait for a pending confirm or prompt dialog after dispatch")
	act.Flags().BoolVar(&actExpectNavigation, "expect-navigation", false, "wait for a main-frame or in-page navigation after dispatch")
	act.Flags().BoolVar(&actExpectDOMChange, "expect-dom-change", false, "wait for the accessibility snapshot to change after dispatch")
	act.Flags().IntVar(&actPostconditionTimeout, "postcondition-timeout", 10_000, "postcondition timeout in milliseconds")
	act.PreRunE = func(cmd *cobra.Command, _ []string) error {
		actNthSet = cmd.Flags().Changed("nth")
		selected := 0
		for _, active := range []bool{actExpectURL != "", actExpectText != "", actExpectDialog, actExpectNavigation, actExpectDOMChange} {
			if active {
				selected++
			}
		}
		if selected > 1 {
			return usageError{errors.New("choose at most one action postcondition")}
		}
		if actPostconditionTimeout < 1 || actPostconditionTimeout > maxBrowserWaitMillis {
			return usageError{fmt.Errorf("--postcondition-timeout must be between 1 and %d milliseconds", maxBrowserWaitMillis)}
		}
		return nil
	}
	cmd.AddCommand(act)

	var clickHuman bool
	click := &cobra.Command{
		Use:   "click <ref>",
		Short: "Click an element reference from the latest snapshot",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clickArgs := map[string]any{"ref": args[0]}
			if clickHuman {
				clickArgs["human"] = true
			}
			return ctx.runBrowserAction(cmd, "click", clickArgs, *jsonOutput)
		},
	}
	click.Flags().BoolVar(&clickHuman, "human", false, "approach the element along a curved, human-like pointer path")
	cmd.AddCommand(click)

	for _, action := range []struct {
		name  string
		short string
	}{
		{name: "dblclick", short: "Double-click an element reference from the latest snapshot"},
		{name: "focus", short: "Focus an element reference from the latest snapshot"},
		{name: "scrollintoview", short: "Scroll an element reference into view"},
	} {
		cmd.AddCommand(&cobra.Command{
			Use:   action.name + " <ref>",
			Short: action.short,
			Args:  exactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return ctx.runBrowserAction(cmd, action.name, map[string]any{"ref": args[0]}, *jsonOutput)
			},
		})
	}
}

// addBrowserInputCommands registers the input subcommands of `tallpine browser`.
func addBrowserInputCommands(cmd *cobra.Command, ctx *commandContext, jsonOutput *bool) {
	var dragHuman bool
	drag := &cobra.Command{
		Use:   "drag <source-ref> <target-ref>",
		Short: "Drag one element onto another",
		Args:  exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dragArgs := map[string]any{"ref": args[0], "targetRef": args[1]}
			if dragHuman {
				dragArgs["human"] = true
			}
			return ctx.runBrowserAction(cmd, "drag", dragArgs, *jsonOutput)
		},
	}
	drag.Flags().BoolVar(&dragHuman, "human", false, "move along a curved, human-like pointer path")
	cmd.AddCommand(drag)

	cmd.AddCommand(&cobra.Command{
		Use:   "fill <ref> <text>",
		Short: "Replace the value of a form control",
		Args:  exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runBrowserAction(cmd, "fill", map[string]any{"ref": args[0], "text": args[1]}, *jsonOutput)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "type <ref> <text>",
		Short: "Type text at the current cursor position in a form control",
		Args:  exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runBrowserAction(cmd, "type", map[string]any{"ref": args[0], "text": args[1]}, *jsonOutput)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "press <key>",
		Short: "Press a key or modifier chord such as Enter or Control+A",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runBrowserAction(cmd, "press", map[string]any{"key": args[0]}, *jsonOutput)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "hover <ref>",
		Short: "Move the pointer over an element",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runBrowserAction(cmd, "hover", map[string]any{"ref": args[0]}, *jsonOutput)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "highlight <ref>",
		Short: "Visually highlight an element without changing page state",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runBrowserAction(cmd, "highlight", map[string]any{"ref": args[0]}, *jsonOutput)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "unhighlight",
		Short: "Remove the current element highlight",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runBrowserAction(cmd, "unhighlight", nil, *jsonOutput)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "tabs",
		Short: "List this session's browser tabs",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runBrowserAction(cmd, "tabs", nil, *jsonOutput)
		},
	})

	tabCmd := &cobra.Command{
		Use:   "tab",
		Short: "Create, select, or close a browser tab",
		Args:  noArgs,
	}
	tabCmd.AddCommand(&cobra.Command{
		Use:   "new [url]",
		Short: "Open a new tab, optionally navigating to a URL",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			actionArgs := map[string]any{}
			if len(args) == 1 {
				actionArgs["url"] = args[0]
			}
			return ctx.runBrowserAction(cmd, "tab-new", actionArgs, *jsonOutput)
		},
	})
	tabCmd.AddCommand(&cobra.Command{
		Use:   "select <tab-id>",
		Short: "Make a browser tab active",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runBrowserAction(cmd, "tab-select", map[string]any{"tabId": args[0]}, *jsonOutput)
		},
	})
	tabCmd.AddCommand(&cobra.Command{
		Use:   "close [tab-id]",
		Short: "Close a browser tab, defaulting to the active tab",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			actionArgs := map[string]any{}
			if len(args) == 1 {
				actionArgs["tabId"] = args[0]
			}
			return ctx.runBrowserAction(cmd, "tab-close", actionArgs, *jsonOutput)
		},
	})
	cmd.AddCommand(tabCmd)

	devtoolsCmd := &cobra.Command{
		Use:   "devtools",
		Short: "Open and control Chromium's DevTools for the active page",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runBrowserAction(cmd, "devtools-open", nil, *jsonOutput)
		},
	}
	devtoolsOpen := &cobra.Command{
		Use:   "open",
		Short: "Open the real Chromium DevTools frontend",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runBrowserAction(cmd, "devtools-open", nil, *jsonOutput)
		},
	}
	devtoolsCmd.AddCommand(devtoolsOpen)
	devtoolsCmd.AddCommand(&cobra.Command{
		Use:   "close",
		Short: "Close Chromium DevTools",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runBrowserAction(cmd, "devtools-close", nil, *jsonOutput)
		},
	})
	cmd.AddCommand(devtoolsCmd)
}
