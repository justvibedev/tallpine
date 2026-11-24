package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// The fake desktop browser endpoints the tallpine browser CLI tests talk to.

type browserRequestCapture struct {
	path                   string
	capability             string
	body                   apitypes.BrowserCommandRequest
	annotationsUnavailable bool
	annotationsIgnored     bool
	// result, when set, replaces the fake desktop's answer.
	result string
}

func browserCLIServer(t *testing.T, capture *browserRequestCapture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.path = r.URL.RequestURI()
		capture.capability = r.Header.Get(browserCapabilityHeader)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/browser/status" {
			_, _ = io.WriteString(w, `{"sessionId":"tp-1","connected":true,"backend":"chrome","transport":"electron-webcontents-debugger"}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/browser/commands" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&capture.body); err != nil {
			t.Fatalf("decode command: %v", err)
		}
		result := `{"ok":true}`
		switch capture.body.Action {
		case "snapshot":
			result = `{"text":"button Save [ref=e1]"}`
		case "get":
			result = `{"value":"page text from the document"}`
		case "screenshot":
			result = `{"data":"cG5n","width":10,"height":20}`
			if capture.body.Args["annotate"] == true {
				result = `{"data":"cG5n","width":10,"height":20,"annotations":[{"number":1,"ref":"e1","role":"button","name":"Save"}]}`
				if capture.annotationsUnavailable {
					result = `{"data":"cG5n","width":10,"height":20,"annotationsUnavailable":true}`
				}
				if capture.annotationsIgnored {
					result = `{"data":"cG5n","width":10,"height":20}`
				}
			}
		case "tabs":
			result = `{"activeTabId":"t2","tabs":[{"id":"t1","title":"First","url":"http://localhost:3000/","active":false},{"id":"t2","title":"Second","url":"http://localhost:4173/","active":true}]}`
		case "network-start", "network-status":
			result = `{"active":true,"metadataOnly":true,"tabId":"t1","requestCount":1,"maxEntries":200}`
		case "network-list", "network-stop":
			result = `{"active":false,"metadataOnly":true,"tabId":"t1","requestCount":1,"maxEntries":200,"requests":[{"method":"GET","url":"https://api.example.test/items?token=%5Bredacted%5D","resourceType":"xhr","status":200,"durationMs":42}]}`
		case "network-clear":
			result = `{"active":true,"metadataOnly":true,"tabId":"t1","requestCount":0,"maxEntries":200}`
		}
		if capture.result != "" {
			result = capture.result
		}
		_, _ = io.WriteString(w, `{"requestId":"r1","sessionId":"tp-1","action":"`+capture.body.Action+`","result":`+result+`}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setBrowserIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("TALLPINE_SESSION_ID", "tp-1")
	t.Setenv("TALLPINE_BROWSER_CAPABILITY", "capability-1")
}
