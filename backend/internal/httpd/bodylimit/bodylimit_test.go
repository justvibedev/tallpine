package bodylimit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func readAll(w http.ResponseWriter, r *http.Request) {
	if _, err := io.ReadAll(r.Body); err != nil {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func TestOversizeBodyIs413(t *testing.T) {
	h := Middleware(16)(http.HandlerFunc(readAll))
	for name, body := range map[string]io.Reader{
		"known length": strings.NewReader(strings.Repeat("a", 100)),
		"chunked":      io.MultiReader(strings.NewReader(strings.Repeat("a", 100))),
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", body))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: status = %d, want 413", name, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("ok")))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("small body: status = %d", rec.Code)
	}
}

func TestRaiseLiftsDefaultCap(t *testing.T) {
	h := Middleware(16)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Raise(w, r, 1024)
		readAll(w, r)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 500))))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("raised route: status = %d, want 204", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 2000))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("raised cap exceeded: status = %d, want 400", rec.Code)
	}
}

// A real server with a short body deadline: streams (no body) must outlive it,
// a stalled body upload must be cut off.
func TestBodyReadDeadlineSparesStreams(t *testing.T) {
	h := BodyReadDeadline(150 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			select {
			case <-time.After(500 * time.Millisecond):
				w.WriteHeader(http.StatusOK)
			case <-r.Context().Done():
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("stream cut off: res=%v err=%v", res, err)
	}
	_ = res.Body.Close()

	pr, pw := io.Pipe()
	go func() { _, _ = pw.Write([]byte("x")); time.Sleep(time.Second); _ = pw.Close() }()
	res, err = http.Post(srv.URL, "application/octet-stream", pr)
	if err == nil {
		defer res.Body.Close()
		if res.StatusCode != http.StatusRequestTimeout {
			t.Fatalf("stalled upload: status = %d, want 408", res.StatusCode)
		}
	}
}

// A slow but steady upload (total time > d, each chunk < d) must succeed.
func TestBodyReadDeadlineAllowsSlowSteadyUpload(t *testing.T) {
	h := BodyReadDeadline(300 * time.Millisecond)(http.HandlerFunc(readAll))
	srv := httptest.NewServer(h)
	defer srv.Close()
	pr, pw := io.Pipe()
	go func() {
		for i := 0; i < 6; i++ {
			_, _ = pw.Write([]byte("x"))
			time.Sleep(120 * time.Millisecond)
		}
		_ = pw.Close()
	}()
	res, err := http.Post(srv.URL, "application/octet-stream", pr)
	if err != nil {
		t.Fatalf("steady upload failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", res.StatusCode)
	}
}
