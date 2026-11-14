package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// addBrowserWaitCommands registers the wait subcommands of `tallpine browser`.
func addBrowserWaitCommands(cmd *cobra.Command, ctx *commandContext, jsonOutput *bool) {
	var scrollAmount int
	scroll := &cobra.Command{
		Use:   "scroll <up|down|left|right>",
		Short: "Scroll the page in one direction",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runBrowserAction(
				cmd,
				"scroll",
				map[string]any{"direction": args[0], "amount": scrollAmount},
				*jsonOutput,
			)
		},
	}
	scroll.Flags().IntVar(&scrollAmount, "amount", 600, "scroll distance in CSS pixels")
	cmd.AddCommand(scroll)

	cmd.AddCommand(&cobra.Command{
		Use:   "select <ref> <value>",
		Short: "Select an option value",
		Args:  exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runBrowserAction(cmd, "select", map[string]any{"ref": args[0], "value": args[1]}, *jsonOutput)
		},
	})

	for _, checked := range []bool{true, false} {
		action := "check"
		short := "Check a checkbox or switch"
		if !checked {
			action = "uncheck"
			short = "Uncheck a checkbox or switch"
		}
		cmd.AddCommand(&cobra.Command{
			Use:   action + " <ref>",
			Short: short,
			Args:  exactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return ctx.runBrowserAction(cmd, action, map[string]any{"ref": args[0]}, *jsonOutput)
			},
		})
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "get <property> [ref]",
		Short: "Read a page or element property",
		Long:  "Read url, title, or text from the page, or text, value, or checked from an element reference.",
		Args:  rangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			actionArgs := map[string]any{"property": args[0]}
			if len(args) == 2 {
				actionArgs["ref"] = args[1]
			}
			return ctx.runBrowserAction(cmd, "get", actionArgs, *jsonOutput)
		},
	})

	var waitText, waitTextGone, waitSelector, waitSelectorGone, waitURL string
	var waitMS, waitStableMS, timeoutMS int
	var waitLoad bool
	waitCmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for page load, DOM stability, time, text, selector, or URL state",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			selected := 0
			for _, active := range []bool{
				waitText != "",
				waitTextGone != "",
				waitSelector != "",
				waitSelectorGone != "",
				waitURL != "",
				waitLoad,
				waitStableMS > 0,
				waitMS > 0,
			} {
				if active {
					selected++
				}
			}
			if selected != 1 {
				return usageError{errors.New(
					"choose exactly one of --text, --text-gone, --selector, --selector-gone, --url, --load, --dom-stable, or --ms",
				)}
			}
			if timeoutMS < 1 || timeoutMS > maxBrowserWaitMillis {
				return usageError{fmt.Errorf("--timeout must be between 1 and %d milliseconds", maxBrowserWaitMillis)}
			}
			if waitMS > maxBrowserWaitMillis {
				return usageError{fmt.Errorf("--ms must not exceed %d milliseconds", maxBrowserWaitMillis)}
			}
			if waitStableMS > maxBrowserWaitMillis {
				return usageError{fmt.Errorf("--dom-stable must not exceed %d milliseconds", maxBrowserWaitMillis)}
			}
			if waitStableMS > timeoutMS {
				return usageError{errors.New("--timeout must be at least as long as --dom-stable")}
			}
			args := map[string]any{"timeoutMs": timeoutMS}
			switch {
			case waitText != "":
				args["text"] = waitText
			case waitTextGone != "":
				args["textGone"] = waitTextGone
			case waitSelector != "":
				args["selector"] = waitSelector
			case waitSelectorGone != "":
				args["selectorGone"] = waitSelectorGone
			case waitURL != "":
				args["url"] = waitURL
			case waitLoad:
				args["load"] = true
			case waitStableMS > 0:
				args["stableMs"] = waitStableMS
			default:
				args["ms"] = waitMS
			}
			return ctx.runBrowserAction(cmd, "wait", args, *jsonOutput)
		},
	}
	waitCmd.Flags().StringVar(&waitText, "text", "", "wait until visible page text contains this value")
	waitCmd.Flags().StringVar(&waitTextGone, "text-gone", "", "wait until visible page text no longer contains this value")
	waitCmd.Flags().StringVar(&waitSelector, "selector", "", "wait until this CSS selector exists")
	waitCmd.Flags().StringVar(&waitSelectorGone, "selector-gone", "", "wait until this CSS selector no longer exists")
	waitCmd.Flags().StringVar(&waitURL, "url", "", "wait until the current URL contains this value")
	waitCmd.Flags().BoolVar(&waitLoad, "load", false, "wait until the current page finishes loading")
	waitCmd.Flags().IntVar(&waitStableMS, "dom-stable", 0, "wait until the DOM has not mutated for this many milliseconds")
	waitCmd.Flags().IntVar(&waitMS, "ms", 0, "wait for a fixed number of milliseconds")
	waitCmd.Flags().IntVar(&timeoutMS, "timeout", 10_000, "condition timeout in milliseconds")
	cmd.AddCommand(waitCmd)

	var viewportDPR float64
	var viewportMobile bool
	viewportCmd := &cobra.Command{
		Use:   "viewport <width>x<height>|reset",
		Short: "emulate a fixed viewport size for the page (or reset it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "reset" {
				if viewportDPR != 0 || viewportMobile {
					return usageError{errors.New("--dpr and --mobile cannot be combined with reset")}
				}
				return ctx.runBrowserAction(cmd, "viewport", map[string]any{"reset": true}, *jsonOutput)
			}
			width, height, err := parseBrowserViewport(args[0])
			if err != nil {
				return usageError{err}
			}
			if viewportDPR < 0 || viewportDPR > 8 {
				return usageError{errors.New("--dpr must be between 0 and 8")}
			}
			actionArgs := map[string]any{"width": width, "height": height, "mobile": viewportMobile}
			if viewportDPR > 0 {
				actionArgs["deviceScaleFactor"] = viewportDPR
			}
			return ctx.runBrowserAction(cmd, "viewport", actionArgs, *jsonOutput)
		},
	}
	viewportCmd.Flags().Float64Var(&viewportDPR, "dpr", 0, "device pixel ratio (default: the display's)")
	viewportCmd.Flags().BoolVar(&viewportMobile, "mobile", false, "emulate a mobile device (meta viewport, touch)")
	cmd.AddCommand(viewportCmd)
}

// addBrowserCaptureCommands registers the capture subcommands of `tallpine browser`.
func addBrowserCaptureCommands(cmd *cobra.Command, ctx *commandContext, jsonOutput *bool) {
	var screenshotBase64, screenshotAnnotate bool
	screenshot := &cobra.Command{
		Use:   "screenshot [path]",
		Short: "Capture the current page to a PNG file",
		Long: "Capture the current page to a PNG file. JSON output writes the file and returns compact metadata.\n" +
			"Without a path, the PNG is written to the system temp directory, not the current directory.\n" +
			"To return inline base64 image data instead, omit the path and use --base64 with --json.\n" +
			"With --annotate, interactive elements are numbered in the image and the matching\n" +
			"snapshot refs are listed alongside it.",
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			if screenshotBase64 && !*jsonOutput {
				return usageError{errors.New("--base64 requires --json")}
			}
			if screenshotBase64 && len(args) != 0 {
				return usageError{errors.New("--base64 cannot be combined with a screenshot path")}
			}
			var actionArgs map[string]any
			if screenshotAnnotate {
				actionArgs = map[string]any{"annotate": true}
			}
			resp, err := ctx.browserAction(cmd.Context(), "screenshot", actionArgs)
			if err != nil {
				return err
			}
			if screenshotBase64 {
				return writeJSON(cmd.OutOrStdout(), resp)
			}
			// An unnamed screenshot is scratch: write it to the temp dir (the
			// session TMPDIR), never the cwd, which is usually the repo (B686).
			path := filepath.Join(os.TempDir(), "tallpine-browser-"+ctx.deps.Now().Format("20060102-150405.000")+".png")
			if len(args) == 1 {
				path = args[0]
			}
			return writeBrowserScreenshot(cmd, browserResultMap(resp.Result), path, *jsonOutput, screenshotAnnotate)
		},
	}
	screenshot.Flags().BoolVar(&screenshotBase64, "base64", false, "include inline base64 image data in JSON output (cannot be used with a path)")
	screenshot.Flags().BoolVar(&screenshotAnnotate, "annotate", false, "number interactive elements in the image and list their snapshot refs")
	cmd.AddCommand(screenshot)

	var networkDuration int
	networkCmd := &cobra.Command{
		Use:   "network",
		Short: "Temporarily capture sanitized network request metadata",
		Args:  noArgs,
	}
	networkStart := &cobra.Command{
		Use:   "start",
		Short: "Start bounded metadata-only capture on the active tab",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if networkDuration < 1 || networkDuration > 300 {
				return usageError{errors.New("--duration must be between 1 and 300 seconds")}
			}
			return ctx.runBrowserAction(
				cmd,
				"network-start",
				map[string]any{"durationSeconds": networkDuration},
				*jsonOutput,
			)
		},
	}
	networkStart.Flags().IntVar(&networkDuration, "duration", 60, "capture duration in seconds (maximum 300)")
	networkCmd.AddCommand(networkStart)
	for _, subcommand := range []struct {
		name  string
		short string
	}{
		{name: "status", short: "Show capture state without enabling it"},
		{name: "list", short: "List captured sanitized request metadata"},
		{name: "stop", short: "Stop capture and list the retained requests"},
		{name: "clear", short: "Clear retained requests without changing capture state"},
	} {
		networkCmd.AddCommand(&cobra.Command{
			Use:   subcommand.name,
			Short: subcommand.short,
			Args:  noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return ctx.runBrowserAction(cmd, "network-"+subcommand.name, nil, *jsonOutput)
			},
		})
	}
	cmd.AddCommand(networkCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "frame <ref|main>",
		Short: "Switch automation into a frame or back to the main document",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runBrowserAction(cmd, "frame", map[string]any{"target": args[0]}, *jsonOutput)
		},
	})

	dialogCmd := &cobra.Command{Use: "dialog", Short: "Inspect or handle a page dialog", Args: noArgs}
	dialogCmd.AddCommand(&cobra.Command{
		Use:   "accept [text]",
		Short: "Accept a dialog, optionally supplying prompt text",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			actionArgs := map[string]any{"operation": "accept"}
			if len(args) == 1 {
				actionArgs["text"] = args[0]
			}
			return ctx.runBrowserAction(cmd, "dialog", actionArgs, *jsonOutput)
		},
	})
	for _, operation := range []string{"dismiss", "status"} {
		dialogCmd.AddCommand(&cobra.Command{
			Use:   operation,
			Short: operation + " the current page dialog",
			Args:  noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return ctx.runBrowserAction(cmd, "dialog", map[string]any{"operation": operation}, *jsonOutput)
			},
		})
	}
	cmd.AddCommand(dialogCmd)

	for _, action := range []string{"console", "errors"} {
		cmd.AddCommand(&cobra.Command{
			Use:   action,
			Short: "Print captured browser " + action,
			Args:  noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return ctx.runBrowserAction(cmd, action, nil, *jsonOutput)
			},
		})
	}
}
