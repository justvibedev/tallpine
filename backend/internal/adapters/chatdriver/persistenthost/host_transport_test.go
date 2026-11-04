package persistenthost

import (
	"context"
	"net"
	"testing"

	"tallpine.eu/tallpine/backend/internal/hostsock"
)

func TestChatHostDialsUnixAndLegacyTCP(t *testing.T) {
	accept := func(ln net.Listener) {
		go func() {
			if c, err := ln.Accept(); err == nil {
				_ = c.Close()
			}
		}()
	}
	if hostsock.Supported {
		ln, addr, err := listenHost(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if !hostsock.IsUnix(addr) {
			t.Fatalf("new hosts must not listen on TCP: %q", addr)
		}
		accept(ln)
		c, err := dialHost(context.Background(), addr)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Close()
		_ = ln.Close()
		if _, err := dialHost(context.Background(), addr); err == nil {
			t.Fatal("dial after host exit must fail (socket removed)")
		}
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	accept(tcp)
	c, err := dialHost(context.Background(), tcp.Addr().String())
	if err != nil {
		t.Fatalf("legacy tcp reattach: %v", err)
	}
	_ = c.Close()
}
