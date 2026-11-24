package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// Handoff limits, shared with the daemon (backend/internal/service/browser/handoff.go).
const (
	maxBrowserHandoffReason  = 500
	maxBrowserHandoffURL     = 2048
	minBrowserHandoffTimeout = 10 * time.Second
	maxBrowserHandoffTimeout = 30 * time.Minute
	// browserHandoffHTTPSlack keeps the HTTP call open past the handoff's own
	// timeout, so the desktop's timeout answer arrives instead of a client cut.
	browserHandoffHTTPSlack = time.Minute
)

// addBrowserHandoffCommand registers `tallpine browser handoff` (B858): the
// agent pauses while the person does a step only they can do, such as a 2FA
// code, a CAPTCHA or a login.
func addBrowserHandoffCommand(cmd *cobra.Command, ctx *commandContext, jsonOutput *bool) {
	var reason, untilURL string
	var timeout time.Duration
	handoff := &cobra.Command{
		Use:   "handoff --reason <text>",
		Short: "Pause until the person does a step only they can do (2FA, CAPTCHA, login)",
		Long: "Hand the browser to the person and wait. The Browser panel shows your reason\n" +
			"with a Continue button and brings the tab to the front, and the person gets a\n" +
			"needs-you notification, on their phone too.\n\n" +
			"Returns when the person clicks Continue, when a page whose URL matches --until-url\n" +
			"loads, or fails with BROWSER_HANDOFF_TIMEOUT after --timeout. --until-url is a glob\n" +
			"matched against the whole URL: * matches any run of characters, ? one character,\n" +
			"for example 'https://app.example.com/dashboard*'.\n\n" +
			"Use it instead of polling `wait` or asking in chat when a page needs the person.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			args, err := browserHandoffArgs(reason, untilURL, timeout)
			if err != nil {
				return err
			}
			return ctx.runBrowserActionWithin(cmd, "handoff", args, *jsonOutput, timeout+browserHandoffHTTPSlack)
		},
	}
	handoff.Flags().StringVar(&reason, "reason", "", "what the person should do, shown in the panel and the notification (required)")
	handoff.Flags().StringVar(&untilURL, "until-url", "", "also return once a page whose URL matches this glob loads")
	handoff.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "give up after this long (10s to 30m)")
	cmd.AddCommand(handoff)
}

func browserHandoffArgs(reason, untilURL string, timeout time.Duration) (map[string]any, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, usageError{errors.New("--reason is required: say what the person should do")}
	}
	if utf8.RuneCountInString(reason) > maxBrowserHandoffReason {
		return nil, usageError{fmt.Errorf("--reason must be at most %d characters", maxBrowserHandoffReason)}
	}
	if timeout < minBrowserHandoffTimeout || timeout > maxBrowserHandoffTimeout {
		return nil, usageError{fmt.Errorf("--timeout must be between %s and %s", minBrowserHandoffTimeout, maxBrowserHandoffTimeout)}
	}
	args := map[string]any{"reason": reason, "timeoutMs": timeout.Milliseconds()}
	if untilURL = strings.TrimSpace(untilURL); untilURL != "" {
		if len(untilURL) > maxBrowserHandoffURL {
			return nil, usageError{fmt.Errorf("--until-url must be at most %d characters", maxBrowserHandoffURL)}
		}
		args["untilUrl"] = untilURL
	}
	return args, nil
}

// writeBrowserHandoffResult says how the handoff ended and where the page is.
func writeBrowserHandoffResult(cmd *cobra.Command, result map[string]any) error {
	outcome, _ := result["outcome"].(string)
	url, _ := result["url"].(string)
	how := "the person clicked Continue"
	if outcome == "url-matched" {
		how = "the page reached --until-url"
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Handoff finished: %s.\nPage: %s\nTake a fresh snapshot before acting.\n", how, url)
	return err
}
