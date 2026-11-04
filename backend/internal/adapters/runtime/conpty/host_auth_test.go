package conpty

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func startAuthServe(t *testing.T, token string) *serveFixture {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	pty := newFakePTY(4242)
	ring := NewRing()
	ring.Append([]byte("SECRET-SCROLLBACK\r\n"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	h := newHost(ServeConfig{SessionID: "auth", Listener: ln, PTY: pty, Ring: ring, Token: token})
	go func() { done <- h.run(ctx) }()
	t.Cleanup(cancel)
	return &serveFixture{host: h, pty: pty, ring: ring, ln: ln, addr: ln.Addr().String(), cancel: cancel, done: done}
}

// expectSilentClose asserts the host sends nothing and closes the connection.
func expectSilentClose(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	b, err := io.ReadAll(conn)
	if len(b) != 0 {
		t.Fatalf("host sent %d bytes to unauthenticated peer: %q", len(b), b)
	}
	if ne := new(net.Error); errors.As(err, ne) && (*ne).Timeout() {
		t.Fatal("host did not close the unauthenticated connection")
	}
}

func TestHostRejectsUnauthenticatedClient(t *testing.T) {
	f := startAuthServe(t, "tok")
	conn, err := net.Dial("tcp", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A status request without the token must get no scrollback and no status.
	frame, _ := EncodeMessage(MsgStatusReq, nil)
	_, _ = conn.Write(frame)
	expectSilentClose(t, conn)
}

func TestHostRejectsWrongToken(t *testing.T) {
	f := startAuthServe(t, "right-token")
	conn, err := net.Dial("tcp", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	frame, _ := EncodeMessage(MsgAuth, []byte("wrong-token"))
	_, _ = conn.Write(frame)
	expectSilentClose(t, conn)
}

func TestHostAcceptsCorrectToken(t *testing.T) {
	f := startAuthServe(t, "right-token")
	conn, err := dialHost(f.addr+"#right-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(conn, hdr); err != nil || hdr[0] != MsgTerminalData {
		t.Fatalf("want scrollback frame, got %v err %v", hdr, err)
	}
}

func TestParserRejectsOversizeFrame(t *testing.T) {
	got := 0
	p := NewMessageParser(func(byte, []byte) { got++ })
	hdr := []byte{MsgTerminalInput, 0xff, 0xff, 0xff, 0xff}
	p.Feed(hdr)
	if !errors.Is(p.Err(), ErrFrameTooLarge) || got != 0 || len(p.buf) != 0 {
		t.Fatalf("err=%v got=%d buf=%d", p.Err(), got, len(p.buf))
	}
}

func TestHostClosesConnOnOversizeFrame(t *testing.T) {
	f := startAuthServe(t, "tok")
	conn, err := dialHost(f.addr+"#tok", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte{MsgTerminalInput, 0x7f, 0xff, 0xff, 0xff})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.Copy(io.Discard, conn) // drains scrollback, then EOF on close
	if ne := new(net.Error); errors.As(err, ne) && (*ne).Timeout() {
		t.Fatal("host kept connection open after oversize frame")
	}
}

func TestReadyLineYieldsTokenAddr(t *testing.T) {
	tok := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	m := readyRE.FindStringSubmatch("READY:12 3456 " + tok)
	if m == nil || hostAddr(m[2], m[3]) != "127.0.0.1:3456#"+tok {
		t.Fatalf("got %v", m)
	}
	if m := readyRE.FindStringSubmatch("READY:12 3456"); m == nil || hostAddr(m[2], m[3]) != "127.0.0.1:3456" {
		t.Fatalf("legacy: %v", m)
	}
}
