//go:build !windows

package conpty

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/hostsock"
)

func readAuth(t *testing.T, c net.Conn, want string) {
	t.Helper()
	var gotType byte
	var got string
	p := NewMessageParser(func(mt byte, payload []byte) { gotType, got = mt, string(payload) })
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, err := c.Read(buf)
	p.Feed(buf[:n])
	if err != nil || gotType != MsgAuth || got != want {
		t.Errorf("auth frame type=%d token=%q err=%v, want token %q", gotType, got, err, want)
	}
}

func TestDialHostUnixSocketSendsToken(t *testing.T) {
	ln, addr, err := hostsock.Listen(filepath.Join(t.TempDir(), "hostsock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := acceptAndReadAuth(t, ln)
	conn, err := dialHostContext(context.Background(), hostAddr(addr, "tok"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-done
}

func TestDialHostLegacyTCPStillWorks(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := acceptAndReadAuth(t, ln)
	port := ln.Addr().(*net.TCPAddr).Port
	conn, err := dialHostContext(context.Background(), hostAddr(itoa(port), "tok"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-done
}

func TestReadyUnixRegexParsesPathWithSpaces(t *testing.T) {
	tok := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	m := readyUnixRE.FindStringSubmatch("READY:42 " + tok + " unix:/Users/a b/hostsock/x.sock")
	if m == nil || m[1] != "42" || hostAddr(m[3], m[2]) != "unix:/Users/a b/hostsock/x.sock#"+tok {
		t.Fatalf("bad parse: %v", m)
	}
}

// acceptAndReadAuth accepts one connection on ln, asserts it carries the "tok"
// auth frame, and closes the returned channel when finished.
func acceptAndReadAuth(t *testing.T, ln net.Listener) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		readAuth(t, c, "tok")
	}()
	return done
}
