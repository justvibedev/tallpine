package browserruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// connectRuntime starts a broker and connects a fake runtime that says hello
// with version.
func connectRuntime(t *testing.T, version int) (*Broker, *json.Encoder, *json.Decoder) {
	t.Helper()
	broker := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = broker.Serve(ctx, ln) }()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(wireMessage{Type: "hello", Version: version}); err != nil {
		t.Fatal(err)
	}
	var reply wireMessage
	if err := dec.Decode(&reply); err != nil || reply.Type != "hello-ok" {
		t.Fatalf("hello reply = %#v, %v", reply, err)
	}
	waitConnected(t, broker)
	return broker, enc, dec
}

// A v3 desktop app ignores Command.Backend and would drive the in-app browser,
// so a chrome command never reaches it (B849 a15); electron commands still do.
func TestBrokerRefusesChromeToV3Runtime(t *testing.T) {
	broker, enc, dec := connectRuntime(t, 3)
	_, err := broker.ExecuteOn(context.Background(), "s1", "chrome", "snapshot", nil)
	var commandErr CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "BROWSER_CHROME_MODE_UNSUPPORTED" {
		t.Fatalf("chrome to v3 = %v, want BROWSER_CHROME_MODE_UNSUPPORTED", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := broker.ExecuteOn(context.Background(), "s1", "electron", "snapshot", nil)
		done <- err
	}()
	var command wireMessage
	if err := dec.Decode(&command); err != nil {
		t.Fatal(err)
	}
	if command.Action != "snapshot" {
		t.Fatalf("first frame after the refusal = %#v, want the electron command", command)
	}
	_ = enc.Encode(wireMessage{Type: "result", RequestID: command.RequestID, OK: true, Result: json.RawMessage(`{}`)})
	if err := waitErr(t, done); err != nil {
		t.Fatalf("electron to v3: %v", err)
	}
}

// A v4 runtime receives the backend on the command frame.
func TestBrokerPassesBackendToV4Runtime(t *testing.T) {
	broker, enc, dec := connectRuntime(t, 4)
	done := make(chan error, 1)
	go func() {
		_, err := broker.ExecuteOn(context.Background(), "s1", "chrome", "click", map[string]interface{}{"target": "e1"})
		done <- err
	}()
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		t.Fatal(err)
	}
	var command Command
	if err := json.Unmarshal(raw, &command); err != nil {
		t.Fatal(err)
	}
	if command.Backend != "chrome" || command.Action != "click" || command.SessionID != "s1" {
		t.Fatalf("command = %s", raw)
	}
	_ = enc.Encode(wireMessage{Type: "result", RequestID: command.RequestID, OK: true, Result: json.RawMessage(`{}`)})
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(browserRuntimeTestTimeout()):
		t.Fatal("timed out waiting for the command")
		return nil
	}
}
