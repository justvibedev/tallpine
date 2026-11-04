package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

const browserCapabilityHeader = "X-AO-Browser-Capability"

const maxBrowserWaitMillis = 55_000

const (
	browserUntrustedBegin = "<<<BEGIN UNTRUSTED EXTERNAL CONTENT>>>"
	browserUntrustedEnd   = "<<<END UNTRUSTED EXTERNAL CONTENT>>>"
)

func newBrowserCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "browser",
		Short: "Inspect and control this Tallpine session's shared desktop browser",
		Long: "Inspect and control the target-isolated browser owned by the current Tallpine session.\n\n" +
			"The desktop app must be open. Commands operate the same live page the user sees,\n" +
			"including while the Browser panel is hidden.",
		Args: noArgs,
	}
	cmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "print the structured response as JSON")

	addBrowserCoreCommands(cmd, ctx, &jsonOutput)
	addBrowserInputCommands(cmd, ctx, &jsonOutput)
	addBrowserWaitCommands(cmd, ctx, &jsonOutput)
	addBrowserCaptureCommands(cmd, ctx, &jsonOutput)
	return cmd
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	}
}

func rangeArgs(minimum, maximum int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.RangeArgs(minimum, maximum)(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	}
}

func currentBrowserIdentity() (string, string, error) {
	sessionID := strings.TrimSpace(brand.Getenv("SESSION_ID"))
	if sessionID == "" {
		return "", "", usageError{errors.New("tallpine browser must run inside a Tallpine session (TALLPINE_SESSION_ID is not set)")}
	}
	capability := strings.TrimSpace(brand.Getenv("BROWSER_CAPABILITY"))
	if capability == "" {
		return "", "", usageError{errors.New("tallpine browser requires the owning session capability (TALLPINE_BROWSER_CAPABILITY is not set)")}
	}
	return sessionID, capability, nil
}

func (c *commandContext) browserStatus(ctx context.Context) (apitypes.BrowserStatusResponse, error) {
	sessionID, capability, err := currentBrowserIdentity()
	if err != nil {
		return apitypes.BrowserStatusResponse{}, err
	}
	var out apitypes.BrowserStatusResponse
	err = c.doJSONPathWithHeaders(
		ctx,
		http.MethodGet,
		"/api/v1/browser/status?sessionId="+url.QueryEscape(sessionID),
		nil,
		&out,
		map[string]string{browserCapabilityHeader: capability},
	)
	return out, err
}

func (c *commandContext) browserAction(ctx context.Context, action string, args map[string]any) (apitypes.BrowserCommandResponse, error) {
	sessionID, capability, err := currentBrowserIdentity()
	if err != nil {
		return apitypes.BrowserCommandResponse{}, err
	}
	var out apitypes.BrowserCommandResponse
	err = c.doJSONPathWithHeaders(
		ctx,
		http.MethodPost,
		"/api/v1/browser/commands",
		apitypes.BrowserCommandRequest{SessionID: domain.SessionID(sessionID), Action: action, Args: args},
		&out,
		map[string]string{browserCapabilityHeader: capability},
	)
	return out, err
}

func (c *commandContext) runBrowserAction(cmd *cobra.Command, action string, args map[string]any, jsonOutput bool) error {
	resp, err := c.browserAction(cmd.Context(), action, args)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeJSON(cmd.OutOrStdout(), resp)
	}
	return writeBrowserResult(cmd, action, browserResultMap(resp.Result))
}

const maxBrowserViewportDimension = 10000

// parseBrowserViewport parses "<width>x<height>" in CSS pixels.
func parseBrowserViewport(value string) (int, int, error) {
	w, h, ok := strings.Cut(strings.ToLower(value), "x")
	width, werr := strconv.Atoi(w)
	height, herr := strconv.Atoi(h)
	if !ok || werr != nil || herr != nil || width < 1 || height < 1 ||
		width > maxBrowserViewportDimension || height > maxBrowserViewportDimension {
		return 0, 0, fmt.Errorf("viewport must be <width>x<height> in CSS pixels (1-%d), or \"reset\"", maxBrowserViewportDimension)
	}
	return width, height, nil
}

// browserResultMap narrows a browser command result to the object the action
// writers read; any other shape reads as empty.
func browserResultMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
