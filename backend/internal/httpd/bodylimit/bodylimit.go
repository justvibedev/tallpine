// Package bodylimit caps request bodies for every route by default and lets
// individual handlers opt into a larger limit.
//
// A bare http.MaxBytesReader wrapped around an already-limited body can only
// tighten the limit, so handlers that legitimately accept big payloads
// (attachments, conversation images, artifact files) call Raise instead of
// wrapping r.Body themselves.
package bodylimit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Default is the body cap applied to routes that do not opt in.
const Default int64 = 1 << 20

type ctxKey struct{}

// tracker records whether a request body hit its cap, shared by the
// middleware, Raise and the response writer.
type tracker struct {
	orig     io.ReadCloser
	exceeded atomic.Bool
}

// limited wraps orig in http.MaxBytesReader and notes when the cap is hit.
func (t *tracker) limited(w http.ResponseWriter, limit int64) io.ReadCloser {
	return &trackedBody{ReadCloser: http.MaxBytesReader(w, t.orig, limit), t: t}
}

type trackedBody struct {
	io.ReadCloser
	t *tracker
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		b.t.exceeded.Store(true)
	}
	return n, err
}

// statusWriter turns the 400 a handler answers after an oversize body into
// 413. Handlers decode with many helpers that all map read errors to 400;
// fixing the status here keeps that in one place.
type statusWriter struct {
	http.ResponseWriter
	t *tracker
}

func (s *statusWriter) WriteHeader(code int) {
	if code == http.StatusBadRequest && s.t.exceeded.Load() {
		code = http.StatusRequestEntityTooLarge
	}
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Flush keeps streaming handlers' direct http.Flusher assertions working.
func (s *statusWriter) Flush() { _ = http.NewResponseController(s.ResponseWriter).Flush() }

// Middleware wraps every request body in http.MaxBytesReader(limit) and
// reports an exceeded cap as 413.
func Middleware(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				t := &tracker{orig: r.Body}
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, t))
				r.Body = t.limited(w, limit)
				w = &statusWriter{ResponseWriter: w, t: t}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Raise replaces the request body with one capped at limit, replacing the
// middleware default. Without the middleware it simply applies limit.
func Raise(w http.ResponseWriter, r *http.Request, limit int64) {
	if r.Body == nil || r.Body == http.NoBody {
		return
	}
	t, ok := r.Context().Value(ctxKey{}).(*tracker)
	if !ok {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		return
	}
	// Opted-in routes keep their historical error shape (400); only the
	// default cap reports 413.
	r.Body = http.MaxBytesReader(w, t.orig, limit)
}

// BodyReadDeadline bounds how long a client may stall while delivering a
// request body (each chunk of progress re-arms it), without the stream-killing side effect of http.Server.ReadTimeout.
//
// ReadTimeout leaves its deadline armed on the connection after the request is
// read; the server's background read then fails on it and cancels the request
// context, which ends SSE and WebSocket streams. This middleware arms the
// deadline per request through http.ResponseController and disarms it as soon
// as the body is fully read (or immediately for bodiless requests such as
// stream GETs and upgrades), so only slow body uploads are cut off.
func BodyReadDeadline(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := http.NewResponseController(w)
			if r.Body == nil || r.Body == http.NoBody {
				_ = rc.SetReadDeadline(time.Time{})
				next.ServeHTTP(w, r)
				return
			}
			if rc.SetReadDeadline(time.Now().Add(d)) == nil {
				r.Body = &deadlineBody{ReadCloser: r.Body, rc: rc, d: d}
			}
			next.ServeHTTP(w, r)
		})
	}
}

type deadlineBody struct {
	io.ReadCloser
	rc    *http.ResponseController
	d     time.Duration
	clear sync.Once
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.disarm()
	} else if n > 0 {
		// Progress re-arms the deadline: it bounds stalls, not total upload time.
		_ = b.rc.SetReadDeadline(time.Now().Add(b.d))
	}
	return n, err
}

func (b *deadlineBody) Close() error {
	b.disarm()
	return b.ReadCloser.Close()
}

func (b *deadlineBody) disarm() {
	b.clear.Do(func() { _ = b.rc.SetReadDeadline(time.Time{}) })
}
