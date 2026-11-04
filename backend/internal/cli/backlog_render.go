package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func writeBacklogTable(w io.Writer, items []apitypes.BacklogItemResponse) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "REF\tSTATE\tPARENT\tHOLD\tTITLE")
	for _, it := range items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", it.Ref, it.State, dash(it.ParentRef), dash(it.Hold), it.Title)
	}
	return tw.Flush()
}

func writeBacklogItem(w io.Writer, it apitypes.BacklogItemResponse) error {
	p := func(label, value string) {
		if value != "" {
			_, _ = fmt.Fprintf(w, "%-10s %s\n", label+":", value)
		}
	}
	p("ref", it.Ref)
	p("title", it.Title)
	p("state", it.State)
	p("hold", it.Hold)
	p("scope", it.Scope)
	p("parent", it.ParentRef)
	if it.ChildCount > 0 {
		p("children", fmt.Sprintf("%d", it.ChildCount))
	}
	p("done when", it.DoneWhen)
	origin := it.Origin.Kind
	if it.Origin.SessionID != "" {
		origin += " (" + it.Origin.SessionID + ")"
	}
	p("origin", origin)
	p("excerpt", it.Origin.Excerpt)
	p("closed", it.ClosedReason)
	p("auto-close", it.AutoClose)
	if len(it.Fields) > 0 {
		keys := make([]string, 0, len(it.Fields))
		for k := range it.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		_, _ = fmt.Fprintln(w, "fields:")
		for _, k := range keys {
			_, _ = fmt.Fprintf(w, "  %s=%s\n", k, it.Fields[k])
		}
	}
	if len(it.Links) > 0 {
		_, _ = fmt.Fprintln(w, "links:")
		for _, l := range it.Links {
			_, _ = fmt.Fprintf(w, "  %s %s\n", l.Kind, l.Display)
		}
	}
	if strings.TrimSpace(it.Note) != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", strings.TrimSpace(it.Note))
	}
	if strings.TrimSpace(it.AgentNotes) != "" {
		_, _ = fmt.Fprintf(w, "\nagent notes:\n%s\n", strings.TrimSpace(it.AgentNotes))
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
