package httpd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
)

const testStreamOrigin = "app://renderer"

// streamTestRouter mounts the stream socket on a router whose middleware
// demands a token, with a few SSE handlers behind it.
func streamTestRouter(t *testing.T, closed chan<- string) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Header.Get("X-Test-Token") != "secret" {
				http.Error(w, `{"error":"token"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/api/v1/notifications/stream", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = fmt.Fprintf(w, ": heartbeat\n\n")
		_, _ = fmt.Fprintf(w, "id: 7\nevent: notification_created\ndata: {\"a\":1}\n\n")
		// One frame split across writes, with two data lines.
		_, _ = fmt.Fprintf(w, "event: multi\ndata: one\n")
		_, _ = fmt.Fprintf(w, "data: two\n\n")
		_, _ = fmt.Fprintf(w, "data: last=%s accept=%s\n\n", req.Header.Get("Last-Event-ID"), req.Header.Get("Accept"))
	})
	r.Get("/api/v1/events", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
		closed <- req.URL.RawQuery
	})
	r.Get("/api/v1/sessions/{sessionId}/workspace/events", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	mountEventStreams(r, r, discardLogger(), []string{testStreamOrigin})
	return r
}

func dialStreams(t *testing.T, srv *httptest.Server, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+eventStreamsPath, &websocket.DialOptions{HTTPHeader: header})
}

func readStreamMessages(t *testing.T, c *websocket.Conn, n int) []eventStreamServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messages := make([]eventStreamServerMessage, 0, n)
	for len(messages) < n {
		var message eventStreamServerMessage
		if err := wsjson.Read(ctx, c, &message); err != nil {
			t.Fatalf("read message %d: %v (got %+v)", len(messages), err, messages)
		}
		messages = append(messages, message)
	}
	return messages
}

func strPtr(s string) *string { return &s }

func TestEventStreamsCarrySSEFramesWithTheUpgradeHeaders(t *testing.T) {
	srv := httptest.NewServer(streamTestRouter(t, make(chan string, 1)))
	defer srv.Close()
	c, _, err := dialStreams(t, srv, http.Header{"Origin": {testStreamOrigin}, "X-Test-Token": {"secret"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()
	ctx := context.Background()
	if err := wsjson.Write(ctx, c, eventStreamClientMessage{Op: "open", ID: "n1", Path: "/api/v1/notifications/stream", LastEventID: "6"}); err != nil {
		t.Fatal(err)
	}
	got := readStreamMessages(t, c, 5)
	want := []eventStreamServerMessage{
		{ID: "n1", Type: "open"},
		{ID: "n1", Type: "event", Event: "notification_created", Data: strPtr(`{"a":1}`), LastID: strPtr("7")},
		{ID: "n1", Type: "event", Event: "multi", Data: strPtr("one\ntwo")},
		{ID: "n1", Type: "event", Data: strPtr("last=6 accept=text/event-stream")},
		{ID: "n1", Type: "end"},
	}
	for i := range want {
		if fmt.Sprintf("%+v", deref(got[i])) != fmt.Sprintf("%+v", deref(want[i])) {
			t.Errorf("message %d = %+v, want %+v", i, deref(got[i]), deref(want[i]))
		}
	}
}

func deref(m eventStreamServerMessage) map[string]any {
	out := map[string]any{"id": m.ID, "type": m.Type, "event": m.Event, "retry": m.Retry, "status": m.Status}
	if m.Data != nil {
		out["data"] = *m.Data
	}
	if m.LastID != nil {
		out["lastEventId"] = *m.LastID
	}
	return out
}

func TestEventStreamsReportFailuresAndRefuseOtherPaths(t *testing.T) {
	srv := httptest.NewServer(streamTestRouter(t, make(chan string, 1)))
	defer srv.Close()
	c, _, err := dialStreams(t, srv, http.Header{"Origin": {testStreamOrigin}, "X-Test-Token": {"secret"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()
	ctx := context.Background()
	for _, open := range []eventStreamClientMessage{
		{Op: "open", ID: "missing", Path: "/api/v1/sessions/s-1/workspace/events"},
		{Op: "open", ID: "rest", Path: "/api/v1/projects"},
		{Op: "open", ID: "escape", Path: "/api/v1/sessions/../projects/workspace/events"},
		{Op: "open", ID: "absolute", Path: "http://evil.example/api/v1/events"},
	} {
		if err := wsjson.Write(ctx, c, open); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]int{}
	for _, message := range readStreamMessages(t, c, 4) {
		if message.Type != "error" {
			t.Fatalf("unexpected %+v", message)
		}
		got[message.ID] = message.Status
	}
	want := map[string]int{"missing": http.StatusNotFound, "rest": http.StatusBadRequest, "escape": http.StatusBadRequest, "absolute": http.StatusBadRequest}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("errors = %v, want %v", got, want)
	}
}

func TestEventStreamsCloseCancelsTheStream(t *testing.T) {
	closed := make(chan string, 2)
	srv := httptest.NewServer(streamTestRouter(t, closed))
	defer srv.Close()
	c, _, err := dialStreams(t, srv, http.Header{"Origin": {testStreamOrigin}, "X-Test-Token": {"secret"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, eventStreamClientMessage{Op: "open", ID: "e1", Path: "/api/v1/events?after=latest"})
	_ = wsjson.Write(ctx, c, eventStreamClientMessage{Op: "open", ID: "e2", Path: "/api/v1/events?after=9"})
	readStreamMessages(t, c, 2)
	_ = wsjson.Write(ctx, c, eventStreamClientMessage{Op: "close", ID: "e1"})
	select {
	case query := <-closed:
		if query != "after=latest" {
			t.Fatalf("closed %q, want the stream that was closed", query)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("closing a stream did not cancel its handler")
	}
	// Dropping the socket ends the rest.
	_ = c.Close(websocket.StatusNormalClosure, "")
	select {
	case query := <-closed:
		if query != "after=9" {
			t.Fatalf("closed %q", query)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("closing the socket did not cancel its streams")
	}
}

func TestEventStreamsRefuseForeignOriginsAndRunMiddlewarePerStream(t *testing.T) {
	srv := httptest.NewServer(streamTestRouter(t, make(chan string, 1)))
	defer srv.Close()
	for _, origin := range []string{"http://evil.example", "http://ao-preview.mfxs2mi.localhost:3101", "null"} {
		if _, resp, err := dialStreams(t, srv, http.Header{"Origin": {origin}, "X-Test-Token": {"secret"}}); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("origin %s: err=%v resp=%v, want 403", origin, err, resp)
		}
	}
	// Without the token the upgrade itself is refused by the middleware.
	if _, resp, err := dialStreams(t, srv, http.Header{"Origin": {testStreamOrigin}}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: err=%v resp=%v, want 401", err, resp)
	}
}

func TestEventStreamsOnTheDaemonRouterRequireTheToken(t *testing.T) {
	router := NewRouterWithControl(config.Config{AllowedOrigins: []string{testStreamOrigin}}, discardLogger(), nil, APIDeps{}, ControlDeps{DaemonToken: "install-token"})
	srv := httptest.NewServer(router)
	defer srv.Close()
	if _, resp, err := dialStreams(t, srv, http.Header{"Origin": {testStreamOrigin}}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: err=%v resp=%v, want 401", err, resp)
	}
	c, _, err := dialStreams(t, srv, http.Header{"Origin": {testStreamOrigin}, "X-VO-Token": {"install-token"}})
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	defer c.CloseNow()
	// The stream is served by the real route; with no CDC source wired it answers 501.
	_ = wsjson.Write(context.Background(), c, eventStreamClientMessage{Op: "open", ID: "cdc", Path: "/api/v1/events?after=latest"})
	got := readStreamMessages(t, c, 1)[0]
	if got.Type != "error" || got.Status != http.StatusNotImplemented {
		t.Fatalf("got %+v, want the route's 501", got)
	}
}

func TestEventStreamsCloseWhenTheServerShutsDown(t *testing.T) {
	closed := make(chan string, 1)
	shutdown := make(chan struct{})
	srv := httptest.NewUnstartedServer(streamTestRouter(t, closed))
	srv.Config.BaseContext = func(net.Listener) context.Context {
		return context.WithValue(context.Background(), eventStreamsShutdownKey{}, (<-chan struct{})(shutdown))
	}
	srv.Start()
	defer srv.Close()
	c, _, err := dialStreams(t, srv, http.Header{"Origin": {testStreamOrigin}, "X-Test-Token": {"secret"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()
	_ = wsjson.Write(context.Background(), c, eventStreamClientMessage{Op: "open", ID: "e1", Path: "/api/v1/events?after=1"})
	readStreamMessages(t, c, 1)
	close(shutdown)
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not end the stream")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var message eventStreamServerMessage
	// Cancelling the socket's read tears the connection down; the client
	// reconnects either way.
	if err := wsjson.Read(ctx, c, &message); err == nil || ctx.Err() != nil {
		t.Fatalf("read after shutdown: %v, want the socket closed", err)
	}
}

func TestEventStreamsKeepTheUpgradePeer(t *testing.T) {
	seen := make(chan daemonsock.Peer, 1)
	r := chi.NewRouter()
	r.Get("/api/v1/events", func(w http.ResponseWriter, req *http.Request) {
		peer, _ := daemonsock.PeerFrom(req.Context())
		seen <- peer
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
	})
	mountEventStreams(r, r, discardLogger(), []string{testStreamOrigin})
	srv := httptest.NewUnstartedServer(r)
	srv.Config.ConnContext = daemonsock.ContextWithConn
	srv.Start()
	defer srv.Close()
	c, _, err := dialStreams(t, srv, http.Header{"Origin": {testStreamOrigin}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()
	if err := wsjson.Write(context.Background(), c, eventStreamClientMessage{Op: "open", ID: "e1", Path: "/api/v1/events"}); err != nil {
		t.Fatal(err)
	}
	select {
	case peer := <-seen:
		if peer.Transport != daemonsock.TransportTCP {
			t.Fatalf("stream lost its connection's peer: %+v", peer)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream never opened")
	}
}
