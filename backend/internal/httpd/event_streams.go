package httpd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// eventStreamsPath carries the daemon's SSE streams over one WebSocket.
//
// Chromium opens at most six HTTP/1.1 connections per host, and an SSE stream
// holds one for as long as it is open. The desktop keeps three global streams
// open, plus one per viewed session and one per open side chat, which left one
// or two connections for every REST request: a slow response then stalled all
// the others. WebSockets do not count against that pool.
//
// The client opens a stream by its ordinary path. The daemon serves the path
// through its own router, so the token, agent scope and every middleware apply
// to each stream exactly as to a direct request, and turns the SSE frames the
// handler writes into WebSocket messages.
const eventStreamsPath = "/api/v1/streams"

const (
	eventStreamsReadLimit    = 64 << 10
	eventStreamsMaxStreams   = 64
	eventStreamsMaxIDLength  = 64
	eventStreamsWriteTimeout = 10 * time.Second
)

// The SSE routes a stream may open. Anything else is refused, so the socket
// cannot be used to issue arbitrary requests.
var eventStreamRoutes = []*regexp.Regexp{
	regexp.MustCompile(`^/api/v1/events$`),
	regexp.MustCompile(`^/api/v1/notifications/stream$`),
	regexp.MustCompile(`^/api/v1/agents/codex/accounts/events$`),
	regexp.MustCompile(`^/api/v1/sessions/[^/]+/workspace/events$`),
	regexp.MustCompile(`^/api/v1/sessions/[^/]+/conversation/side-chats/[^/]+/events$`),
}

// streamShutdownMiddleware cancels the context of long-lived SSE requests
// (the eventStreamRoutes) when the server starts shutting down, so they end
// and clients reconnect. Every other request keeps its context: Shutdown must
// let in-flight work drain.
func streamShutdownMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shutdown, ok := r.Context().Value(eventStreamsShutdownKey{}).(<-chan struct{})
		if !ok || !eventStreamRouteAllowed(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			select {
			case <-shutdown:
				cancel()
			case <-ctx.Done():
			}
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// eventStreamsShutdownKey carries a channel the loopback server closes when it
// starts shutting down.
type eventStreamsShutdownKey struct{}

// eventStreamClientMessage opens or closes one stream. An id is never reused:
// the client opens each attempt under a new id, so a late message from a
// closed stream cannot be mistaken for its successor.
type eventStreamClientMessage struct {
	Op          string `json:"op"`
	ID          string `json:"id"`
	Path        string `json:"path,omitempty"`
	LastEventID string `json:"lastEventId,omitempty"`
}

// eventStreamServerMessage reports one stream's progress, mirroring what an
// EventSource observes: "open" once the response is a 200 event stream,
// "event" per SSE frame, "error" when the response is not an event stream
// (EventSource fails without retrying), and "end" when the server finished
// the stream (EventSource reconnects).
type eventStreamServerMessage struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Event  string  `json:"event,omitempty"`
	Data   *string `json:"data,omitempty"`
	LastID *string `json:"lastEventId,omitempty"`
	Retry  int     `json:"retry,omitempty"`
	Status int     `json:"status,omitempty"`
}

// mountEventStreams registers the stream socket. root is the daemon's full
// handler: each stream is served through it.
func mountEventStreams(r chi.Router, root http.Handler, log *slog.Logger, allowedOrigins []string) {
	allowed := exactAllowedOrigins(allowedOrigins)
	r.Get(eventStreamsPath, func(w http.ResponseWriter, req *http.Request) {
		// The same origin rule as the terminal socket: a WebSocket is not
		// subject to CORS, so only the app's own origins may open one.
		if !terminalMuxRequestAllowed(req, allowed) {
			w.Header().Add("Vary", "Origin")
			w.Header().Add("Vary", "Sec-Fetch-Site")
			envelope.WriteAPIError(w, req, http.StatusForbidden, "forbidden", "ORIGIN_FORBIDDEN", "Origin is not allowed to open event streams", nil)
			return
		}
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			log.Warn("event streams: websocket upgrade failed", "err", err)
			return
		}
		conn.SetReadLimit(eventStreamsReadLimit)
		serveEventStreams(req, conn, root)
	})
}

type eventStreamSession struct {
	upgrade *http.Request
	root    http.Handler
	conn    *websocket.Conn
	ctx     context.Context
	out     chan eventStreamServerMessage

	mu      sync.Mutex
	streams map[string]context.CancelFunc
	wg      sync.WaitGroup
}

func serveEventStreams(upgrade *http.Request, conn *websocket.Conn, root http.Handler) {
	// Streams inherit nothing from the upgrade request's context but the LAN
	// connection auth records a credential on; they end with the socket.
	// Each stream re-enters the router; it still belongs to the upgrade's peer.
	base := daemonsock.WithPeerFrom(context.WithValue(context.Background(), lanConnContextKey{}, upgrade.Context().Value(lanConnContextKey{})), upgrade.Context())
	ctx, cancel := context.WithCancel(base)
	defer context.AfterFunc(upgrade.Context(), cancel)()
	if shutdown, ok := upgrade.Context().Value(eventStreamsShutdownKey{}).(<-chan struct{}); ok {
		go func() {
			select {
			case <-shutdown:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	s := &eventStreamSession{
		upgrade: upgrade,
		root:    root,
		conn:    conn,
		ctx:     ctx,
		out:     make(chan eventStreamServerMessage, 256),
		streams: map[string]context.CancelFunc{},
	}
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		s.writeLoop(cancel)
	}()
	s.readLoop()
	cancel()
	s.wg.Wait()
	<-writerDone
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// writeLoop is the socket's only writer, so messages of one stream keep their order.
func (s *eventStreamSession) writeLoop(cancel context.CancelFunc) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case message := <-s.out:
			writeCtx, done := context.WithTimeout(s.ctx, eventStreamsWriteTimeout)
			err := wsjson.Write(writeCtx, s.conn, message)
			done()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

func (s *eventStreamSession) send(message eventStreamServerMessage) error {
	if message.Data != nil && s.ctx.Value(lanConnContextKey{}) != nil && !isDesktopClient(s.upgrade.Header) {
		rewritten := string(bytes.ReplaceAll([]byte(*message.Data), desktopSessionLink, mobileSessionLink))
		message.Data = &rewritten
	}
	select {
	case s.out <- message:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *eventStreamSession) readLoop() {
	for {
		var message eventStreamClientMessage
		if err := wsjson.Read(s.ctx, s.conn, &message); err != nil {
			return
		}
		if message.ID == "" || len(message.ID) > eventStreamsMaxIDLength {
			continue
		}
		switch message.Op {
		case "open":
			s.open(message)
		case "close":
			s.mu.Lock()
			if stop, ok := s.streams[message.ID]; ok {
				stop()
				delete(s.streams, message.ID)
			}
			s.mu.Unlock()
		}
	}
}

func (s *eventStreamSession) open(message eventStreamClientMessage) {
	s.mu.Lock()
	_, duplicate := s.streams[message.ID]
	full := len(s.streams) >= eventStreamsMaxStreams
	s.mu.Unlock()
	if duplicate {
		return
	}
	if full {
		_ = s.send(eventStreamServerMessage{ID: message.ID, Type: "error", Status: http.StatusTooManyRequests})
		return
	}
	streamCtx, stop := context.WithCancel(s.ctx)
	req, err := eventStreamRequest(streamCtx, s.upgrade, message.Path, message.LastEventID)
	if err != nil {
		stop()
		_ = s.send(eventStreamServerMessage{ID: message.ID, Type: "error", Status: http.StatusBadRequest})
		return
	}
	s.mu.Lock()
	s.streams[message.ID] = stop
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			stop()
			s.mu.Lock()
			delete(s.streams, message.ID)
			s.mu.Unlock()
		}()
		writer := &eventStreamWriter{id: message.ID, ctx: streamCtx, send: s.send, header: http.Header{}}
		s.root.ServeHTTP(writer, req)
		writer.finish()
	}()
}

// eventStreamRequest builds the GET a direct EventSource would have sent for
// target: the upgrade's headers (token, origin) without its WebSocket ones.
func eventStreamRequest(ctx context.Context, upgrade *http.Request, target, lastEventID string) (*http.Request, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Fragment != "" || u.RawPath != "" {
		return nil, errors.New("event stream: invalid path")
	}
	if path.Clean(u.Path) != u.Path || !eventStreamRouteAllowed(u.Path) {
		return nil, errors.New("event stream: path is not an event stream")
	}
	if strings.ContainsAny(lastEventID, "\r\n\x00") {
		return nil, errors.New("event stream: invalid last event id")
	}
	req := upgrade.Clone(ctx)
	req.Method = http.MethodGet
	req.URL = &url.URL{Path: u.Path, RawQuery: u.RawQuery}
	req.RequestURI = req.URL.RequestURI()
	req.Body = http.NoBody
	req.ContentLength = 0
	req.Header = upgrade.Header.Clone()
	for _, name := range []string{"Upgrade", "Connection", "Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Extensions", "Sec-Websocket-Protocol"} {
		req.Header.Del(name)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Del("Last-Event-ID")
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	return req, nil
}

func eventStreamRouteAllowed(p string) bool {
	for _, route := range eventStreamRoutes {
		if route.MatchString(p) {
			return true
		}
	}
	return false
}

// eventStreamWriter is the ResponseWriter a stream's handler writes to. It
// parses the SSE body into frames and sends each as one message.
type eventStreamWriter struct {
	id     string
	ctx    context.Context
	send   func(eventStreamServerMessage) error
	header http.Header
	status int
	stream bool
	buf    []byte
}

func (w *eventStreamWriter) Header() http.Header { return w.header }

func (w *eventStreamWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	mediaType, _, _ := strings.Cut(w.header.Get("Content-Type"), ";")
	w.stream = status == http.StatusOK && strings.EqualFold(strings.TrimSpace(mediaType), "text/event-stream")
	if w.stream {
		_ = w.send(eventStreamServerMessage{ID: w.id, Type: "open"})
	}
}

func (w *eventStreamWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if !w.stream {
		// Like EventSource, only the status of a failed response is reported.
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	if err := w.dispatch(); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush sends the headers: frames go out as soon as they are complete.
func (w *eventStreamWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
}

func (w *eventStreamWriter) finish() {
	if w.ctx.Err() != nil {
		return
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	if w.stream {
		_ = w.send(eventStreamServerMessage{ID: w.id, Type: "end"})
		return
	}
	_ = w.send(eventStreamServerMessage{ID: w.id, Type: "error", Status: status})
}

// dispatch sends every complete frame in the buffer. Handlers end lines with
// "\n"; a "\r\n" ending is tolerated.
func (w *eventStreamWriter) dispatch() error {
	for {
		end := bytes.Index(w.buf, []byte("\n\n"))
		if end < 0 {
			return nil
		}
		frame := string(w.buf[:end])
		w.buf = w.buf[end+2:]
		if message, ok := parseSSEFrame(w.id, frame); ok {
			if err := w.send(message); err != nil {
				return err
			}
		}
	}
}

// parseSSEFrame reads one frame as the EventSource parser would. A frame of
// comments only (a heartbeat) produces no message.
func parseSSEFrame(id, frame string) (eventStreamServerMessage, bool) {
	message := eventStreamServerMessage{ID: id, Type: "event"}
	var data []string
	hasData, useful := false, false
	for _, line := range strings.Split(frame, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if found {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			message.Event = value
		case "data":
			data = append(data, value)
			hasData, useful = true, true
		case "id":
			if !strings.ContainsRune(value, 0) {
				lastID := value
				message.LastID = &lastID
				useful = true
			}
		case "retry":
			if retry, err := strconv.Atoi(value); err == nil && retry >= 0 {
				message.Retry = retry
				useful = true
			}
		}
	}
	if hasData {
		joined := strings.Join(data, "\n")
		message.Data = &joined
	}
	return message, useful
}
