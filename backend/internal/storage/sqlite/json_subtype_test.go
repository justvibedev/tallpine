package sqlite

import (
	"database/sql"
	"testing"
)

// The bundled SQLite drops the JSON subtype of a scalar subquery result, so
// json_set(x, p, (SELECT json_group_array(...))) stores a quoted string. The
// runtime statements (queries/app_settings.sql SetHarnessDefault,
// queries/conversations.sql ResolveConversationApproval, migration 0119) must
// keep yielding nested JSON.
func TestJSONFunctionsKeepNestedJSON(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	typeOf := func(q string, args ...any) string {
		var got string
		if err := db.QueryRow(q, args...).Scan(&got); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return got
	}
	cases := []struct {
		name, q, want string
		args          []any
	}{
		{"harness default json(?)", `SELECT json_type(json_set('{}', ?1, json(?2)), ?1)`, "object", []any{"$.claude", `{"a":1}`}},
		{"approval json_patch text", `SELECT json_type(json_patch('{"x":1}', CAST(?1 AS TEXT)), '$.r')`, "object", []any{`{"r":{"k":1}}`}},
		{"0119 steps wrapped in json()", `SELECT json_type(json_set('{"steps":[{"status":"pending"}]}', '$.steps', json((SELECT json_group_array(json_set(value,'$.status','completed')) FROM json_each('{"steps":[{"status":"pending"}]}','$.steps')))), '$.steps')`, "array", nil},
	}
	for _, c := range cases {
		if got := typeOf(c.q, c.args...); got != c.want {
			t.Errorf("%s: json_type = %q, want %q", c.name, got, c.want)
		}
	}
}
