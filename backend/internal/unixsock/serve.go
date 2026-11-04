package unixsock

import (
	"context"
	"errors"
	"net"
)

// Serve accepts connections on ln, running handle for each in its own
// goroutine, until ctx is cancelled (nil return) or Accept fails for another
// reason (that error is returned). It closes ln on cancellation.
func Serve(ctx context.Context, ln net.Listener, handle func(net.Conn)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil //nolint:nilerr // listener closure is the expected shutdown path
			}
			return err
		}
		go handle(conn)
	}
}
