package sqlite

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/cdc"
)

var changeLogInsert = regexp.MustCompile(`(?is)(?:INSERT(?:\s+OR\s+\w+)?|REPLACE)\s+INTO\s+change_log\s*\(([^)]*)\)\s*(VALUES\s*\(|SELECT\s)`)

// topLevelItems splits s on commas outside parentheses and quotes, stopping at
// the closing paren (VALUES form) or at FROM/WHERE/; (SELECT form).
func topLevelItems(s string) []string {
	var items []string
	depth, start := 0, 0
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote:
			if c == '\'' {
				inQuote = false
			}
		case c == '\'':
			inQuote = true
		case c == '(':
			depth++
		case c == ')':
			if depth == 0 {
				return append(items, strings.TrimSpace(s[start:i]))
			}
			depth--
		case c == ',' && depth == 0:
			items = append(items, strings.TrimSpace(s[start:i]))
			start = i + 1
		case c == ';' && depth == 0:
			return append(items, strings.TrimSpace(s[start:i]))
		case depth == 0 && (c == ' ' || c == '\n' || c == '\t') && hasKeywordAt(s, i+1, "FROM", "WHERE"):
			return append(items, strings.TrimSpace(s[start:i]))
		}
	}
	return append(items, strings.TrimSpace(s[start:]))
}

func hasKeywordAt(s string, i int, words ...string) bool {
	for _, w := range words {
		if len(s) >= i+len(w) && strings.EqualFold(s[i:i+len(w)], w) {
			return true
		}
	}
	return false
}

// change_log has no CHECK on event_type (a CHECK failure inside a trigger would
// abort the user's own write), so this test is what stops a typo'd event name
// and keeps cdc.AllEventTypes in step with the triggers.
func TestTriggerEventTypesMatchGoList(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261027203307205)
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'trigger' AND sql LIKE '%change_log%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	known := map[string]bool{}
	for _, e := range cdc.AllEventTypes() {
		known[string(e)] = true
	}
	used := map[string]bool{}
	for rows.Next() {
		var name, sqlText string
		if err := rows.Scan(&name, &sqlText); err != nil {
			t.Fatal(err)
		}
		matches := changeLogInsert.FindAllStringSubmatchIndex(sqlText, -1)
		if len(matches) == 0 {
			t.Errorf("trigger %s references change_log but no insert into it could be parsed; teach this test the new form", name)
			continue
		}
		for _, m := range matches {
			cols := strings.Split(sqlText[m[2]:m[3]], ",")
			idx := -1
			for i, c := range cols {
				if strings.TrimSpace(c) == "event_type" {
					idx = i
				}
			}
			items := topLevelItems(sqlText[m[5]:])
			if idx < 0 || idx >= len(items) {
				t.Errorf("trigger %s: cannot locate event_type in change_log insert", name)
				continue
			}
			lit := items[idx]
			if len(lit) < 3 || lit[0] != '\'' || lit[len(lit)-1] != '\'' {
				t.Errorf("trigger %s: event_type is not a plain literal: %s", name, lit)
				continue
			}
			typ := lit[1 : len(lit)-1]
			used[typ] = true
			if !known[typ] {
				t.Errorf("trigger %s writes event type %q missing from cdc.AllEventTypes", name, typ)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var unused []string
	for typ := range known {
		if !used[typ] {
			unused = append(unused, typ)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("cdc.AllEventTypes lists types no trigger writes: %v", unused)
	}
}
