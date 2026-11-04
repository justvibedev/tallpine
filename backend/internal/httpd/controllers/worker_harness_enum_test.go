package controllers_test

import (
	"reflect"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

// Worker-harness request fields feed the OpenAPI enums that generated clients
// validate against. Each must list every domain.AllHarnesses entry, or the app
// cannot select a harness the daemon supports (DeepSeek went missing this way).
func TestWorkerHarnessRequestEnumsListEveryHarness(t *testing.T) {
	for _, tc := range []struct {
		typ   reflect.Type
		field string
	}{
		{reflect.TypeOf(controllers.SpawnSessionRequest{}), "Harness"},
		{reflect.TypeOf(controllers.DelegateTaskRequest{}), "Agent"},
	} {
		f, ok := tc.typ.FieldByName(tc.field)
		if !ok {
			t.Fatalf("%s.%s missing", tc.typ.Name(), tc.field)
		}
		enum := strings.Split(f.Tag.Get("enum"), ",")
		for _, h := range domain.AllHarnesses {
			found := false
			for _, e := range enum {
				found = found || e == string(h)
			}
			if !found {
				t.Errorf("%s.%s enum %v lacks %q", tc.typ.Name(), tc.field, enum, h)
			}
		}
	}
}
