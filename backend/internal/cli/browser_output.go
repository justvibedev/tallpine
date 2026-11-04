package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type browserScreenshotFileResult struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Annotations []any  `json:"annotations,omitempty"`
	// True when --annotate was requested but the browser returned no usable
	// annotations, so the image may carry no labels.
	AnnotationsUnavailable bool `json:"annotationsUnavailable,omitempty"`
}

func writeBrowserResult(cmd *cobra.Command, action string, result map[string]any) error {
	if action == "act" {
		return writeBrowserActResult(cmd, result)
	}
	if action == "snapshot" {
		if kind, ok := result["kind"].(string); ok {
			return writeBrowserSnapshotDelta(cmd, kind, result)
		}
		if text, ok := result["text"].(string); ok {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), browserUntrustedText(text))
			return err
		}
	}
	if action == "console" || action == "errors" {
		messages, _ := result["messages"].([]any)
		if len(messages) == 0 {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "No browser "+action+" captured.")
			return err
		}
		for _, message := range messages {
			if item, ok := message.(map[string]any); ok {
				level, _ := item["level"].(string)
				text, _ := item["message"].(string)
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", level, browserUntrustedText(text)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if action == "get" {
		if value, ok := result["value"].(string); ok {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), browserUntrustedText(value))
			return err
		}
		if value, ok := result["value"]; ok {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), value)
			return err
		}
	}
	if action == "tabs" {
		tabs, _ := result["tabs"].([]any)
		if len(tabs) == 0 {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "No browser tabs.")
			return err
		}
		var output strings.Builder
		for _, raw := range tabs {
			tab, _ := raw.(map[string]any)
			id, _ := tab["id"].(string)
			title, _ := tab["title"].(string)
			currentURL, _ := tab["url"].(string)
			marker := " "
			if active, _ := tab["active"].(bool); active {
				marker = "*"
			}
			if _, err := fmt.Fprintf(&output, "%s %s\t%s\t%s\n", marker, id, title, currentURL); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintln(
			cmd.OutOrStdout(),
			browserUntrustedText(strings.TrimSuffix(output.String(), "\n")),
		)
		return err
	}
	if strings.HasPrefix(action, "network-") {
		return writeBrowserNetworkResult(cmd, action, result)
	}
	if currentURL, ok := result["url"].(string); ok && currentURL != "" {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), browserUntrustedText(currentURL))
		return err
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), "Browser "+action+" completed.")
	return err
}

func browserUntrustedText(value string) string {
	// Page-controlled text must not be able to inject a delimiter that looks
	// like the end of Tallpine's trust boundary. Escape only exact marker collisions;
	// the surrounding fixed markers remain easy for humans and agents to parse.
	value = strings.ReplaceAll(value, browserUntrustedBegin, `\u003c`+browserUntrustedBegin[1:])
	value = strings.ReplaceAll(value, browserUntrustedEnd, `\u003c`+browserUntrustedEnd[1:])
	return browserUntrustedBegin + "\n" + value + "\n" + browserUntrustedEnd
}

func writeBrowserSnapshotDelta(cmd *cobra.Command, kind string, result map[string]any) error {
	revision := numberInt(result["revision"])
	baseRevision := numberInt(result["baseRevision"])
	out := cmd.OutOrStdout()
	switch kind {
	case "full":
		text, _ := result["text"].(string)
		_, err := fmt.Fprintf(out, "Snapshot revision %d (full):\n%s\n", revision, browserUntrustedText(text))
		return err
	case "unchanged":
		_, err := fmt.Fprintf(out, "Snapshot unchanged since revision %d (now revision %d).\n", baseRevision, revision)
		return err
	case "delta":
		var body []string
		changes, _ := result["changes"].([]any)
		for _, raw := range changes {
			change, _ := raw.(map[string]any)
			op, _ := change["op"].(string)
			ref, _ := change["ref"].(string)
			line := op + " " + ref
			if node, ok := change["node"].(map[string]any); ok {
				role, _ := node["role"].(string)
				name, _ := node["name"].(string)
				line += fmt.Sprintf(" %s %q", role, name)
			}
			body = append(body, line)
		}
		if tree, ok := result["treeChange"].(map[string]any); ok {
			lines, _ := tree["lines"].([]any)
			body = append(body, fmt.Sprintf("replace %d line(s) starting at 0-based line %d with %d line(s):",
				numberInt(tree["deleteCount"]), numberInt(tree["startLine"]), len(lines)))
			for _, line := range lines {
				text, _ := line.(string)
				body = append(body, text)
			}
		}
		if len(body) == 0 {
			_, err := fmt.Fprintf(out,
				"Snapshot delta from revision %d to %d reported no element changes (rerun with --delta --full for the whole tree).\n",
				baseRevision, revision)
			return err
		}
		_, err := fmt.Fprintf(out,
			"Snapshot delta from revision %d to %d (apply to the revision %d tree, or rerun with --delta --full):\n%s\n",
			baseRevision, revision, baseRevision, browserUntrustedText(strings.Join(body, "\n")))
		return err
	default:
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, browserUntrustedText(string(encoded)))
		return err
	}
}

func writeBrowserActResult(cmd *cobra.Command, result map[string]any) error {
	outcome, _ := result["outcome"].(string)
	switch outcome {
	case "matched":
		candidate, _ := result["candidate"].(map[string]any)
		role, _ := candidate["role"].(string)
		name, _ := candidate["name"].(string)
		ref, _ := result["resolvedRef"].(string)
		retried, _ := result["retried"].(bool)
		suffix := ""
		if retried {
			suffix = " (after retrying a stale reference)"
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Element matched; input dispatched: %s [ref=%s]%s\n%s\n", role, ref, suffix, browserUntrustedText(name)); err != nil {
			return err
		}
		if postcondition, ok := result["postcondition"].(map[string]any); ok {
			status, _ := postcondition["status"].(string)
			kind, _ := postcondition["kind"].(string)
			reason, _ := postcondition["reason"].(string)
			if reason != "" {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "Postcondition %s: %s (%s)\n", kind, status, reason)
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Postcondition %s: %s\n", kind, status)
			return err
		}
		if navigation, ok := result["navigation"].(map[string]any); ok {
			status, _ := navigation["status"].(string)
			reason, _ := navigation["reason"].(string)
			if status == "cancelled" {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "Navigation: cancelled (%s)\n", reason)
				return err
			}
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No application postcondition requested; element matching is not proof of application success.")
		return err
	case "ambiguous":
		candidates, _ := result["candidates"].([]any)
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Ambiguous — multiple elements matched equally well:"); err != nil {
			return err
		}
		for _, raw := range candidates {
			candidate, _ := raw.(map[string]any)
			role, _ := candidate["role"].(string)
			name, _ := candidate["name"].(string)
			ref, _ := candidate["ref"].(string)
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  %s [ref=%s]\n%s\n", role, ref, browserUntrustedText(name)); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "Pick a ref and act/click directly, retry with --nth, or refine the instruction.")
		return err
	case "no-match":
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "No element matched that instruction. Latest snapshot:"); err != nil {
			return err
		}
		text, _ := result["snapshot"].(string)
		_, err := fmt.Fprintln(cmd.OutOrStdout(), browserUntrustedText(text))
		return err
	default:
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "Browser act completed.")
		return err
	}
}

func writeBrowserNetworkResult(cmd *cobra.Command, action string, result map[string]any) error {
	if action == "network-clear" {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "Browser network capture cleared.")
		return err
	}
	active, _ := result["active"].(bool)
	state := "inactive"
	if active {
		state = "active"
	}
	tabID, _ := result["tabId"].(string)
	count := numberString(result["requestCount"])
	maxEntries := numberString(result["maxEntries"])
	if count == "" {
		count = "0"
	}
	if maxEntries == "" {
		maxEntries = "200"
	}
	if action == "network-start" || action == "network-status" {
		_, err := fmt.Fprintf(
			cmd.OutOrStdout(),
			"Browser network capture: %s (tab %s, %s/%s requests, metadata only)\n",
			state,
			tabID,
			count,
			maxEntries,
		)
		return err
	}

	requests, _ := result["requests"].([]any)
	if len(requests) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No browser network requests captured.")
		return err
	}
	var output strings.Builder
	for _, raw := range requests {
		request, _ := raw.(map[string]any)
		method, _ := request["method"].(string)
		currentURL, _ := request["url"].(string)
		resourceType, _ := request["resourceType"].(string)
		status := numberString(request["status"])
		if failed, _ := request["failed"].(bool); failed {
			status = "FAILED"
		} else if status == "" {
			status = "PENDING"
		}
		duration := numberString(request["durationMs"])
		if duration != "" {
			duration += "ms"
		} else {
			duration = "-"
		}
		if _, err := fmt.Fprintf(
			&output,
			"%s %s %s %s %s\n",
			method,
			status,
			resourceType,
			duration,
			currentURL,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(
		cmd.OutOrStdout(),
		browserUntrustedText(strings.TrimSuffix(output.String(), "\n")),
	)
	return err
}

func writeBrowserScreenshot(cmd *cobra.Command, result map[string]any, target string, jsonOutput, annotate bool) error {
	encoded, _ := result["data"].(string)
	if encoded == "" {
		return errors.New("browser returned an empty screenshot")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode browser screenshot: %w", err)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("refusing to overwrite existing screenshot %s", abs)
		}
		return err
	}
	written, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	width := numberInt(result["width"])
	height := numberInt(result["height"])
	if jsonOutput {
		annotations, _ := result["annotations"].([]any)
		unavailable := annotationsMissing(result, annotate)
		return writeJSON(cmd.OutOrStdout(), browserScreenshotFileResult{
			Path:                   abs,
			Size:                   int64(written),
			Width:                  width,
			Height:                 height,
			Annotations:            annotations,
			AnnotationsUnavailable: unavailable,
		})
	}
	size := ""
	if width > 0 && height > 0 {
		size = fmt.Sprintf(" (%dx%d)", width, height)
	}
	if _, err = fmt.Fprintf(cmd.OutOrStdout(), "Saved %s%s\n", abs, size); err != nil {
		return err
	}
	return writeBrowserAnnotations(cmd, result, annotate)
}

// An older desktop host ignores an argument it does not know, so a capture that
// came back without annotations is reported rather than passed off as annotated.
func annotationsMissing(result map[string]any, annotate bool) bool {
	if unavailable, _ := result["annotationsUnavailable"].(bool); unavailable {
		return true
	}
	annotations, _ := result["annotations"].([]any)
	return annotate && len(annotations) == 0
}

func writeBrowserAnnotations(cmd *cobra.Command, result map[string]any, annotate bool) error {
	if annotationsMissing(result, annotate) {
		_, err := fmt.Fprintln(cmd.OutOrStdout(),
			"The browser returned no annotations for this capture, so the image may carry no labels.")
		return err
	}
	annotations, _ := result["annotations"].([]any)
	if len(annotations) == 0 {
		return nil
	}
	lines := make([]string, 0, len(annotations))
	for _, raw := range annotations {
		annotation, _ := raw.(map[string]any)
		role, _ := annotation["role"].(string)
		name, _ := annotation["name"].(string)
		ref, _ := annotation["ref"].(string)
		lines = append(lines, fmt.Sprintf("[%d] %s %q (ref=%s)", numberInt(annotation["number"]), role, name, ref))
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Annotated elements:\n%s\n", browserUntrustedText(strings.Join(lines, "\n")))
	return err
}

func numberInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func numberString(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.Itoa(int(n))
	case int:
		return strconv.Itoa(n)
	default:
		return ""
	}
}
