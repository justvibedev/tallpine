package httpd

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"strings"
)

// The upstream App Store app only renders worker-report cards for
// ao://sessions/ links, while the daemon emits tallpine:// (brand rule). Everything
// served on the mobile listener (LAN and tunnel) is rewritten on the way out;
// the loopback listener used by desktop and CLI, and stored data, keep tallpine://.
var (
	desktopSessionLink = []byte("tallpine://sessions/")
	mobileSessionLink  = []byte("ao://sessions/")
)

// desktopClientHeader is set by the desktop app's remote-host proxy
// (frontend/src/main/remote-proxy.ts). Desktop reaches remote hosts through the
// same listener and tunnel as the phone, but parses only tallpine:// links.
const desktopClientHeader = "X-VO-Client"

func isDesktopClient(h http.Header) bool { return h.Get(desktopClientHeader) == "desktop" }

// mobileLinkRewrite rewrites session links in JSON and event-stream responses
// for every client except the desktop app.
func mobileLinkRewrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isDesktopClient(r.Header) {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&linkRewriteWriter{ResponseWriter: w}, r)
	})
}

type linkRewriteWriter struct {
	http.ResponseWriter
	decided bool
	rewrite bool
}

func (w *linkRewriteWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	ct := w.Header().Get("Content-Type")
	w.rewrite = strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/event-stream")
	if w.rewrite {
		w.Header().Del("Content-Length")
	}
}

func (w *linkRewriteWriter) WriteHeader(code int) {
	w.decide()
	w.ResponseWriter.WriteHeader(code)
}

func (w *linkRewriteWriter) Write(p []byte) (int, error) {
	w.decide()
	if !w.rewrite || !bytes.Contains(p, desktopSessionLink) {
		return w.ResponseWriter.Write(p)
	}
	if _, err := w.ResponseWriter.Write(bytes.ReplaceAll(p, desktopSessionLink, mobileSessionLink)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *linkRewriteWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *linkRewriteWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack unsupported")
}

func (w *linkRewriteWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
