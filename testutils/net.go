package testutils

import (
	"context"
	"errors"
	"net"
	"time"
)

// WaitSuccessfulDial waits for a dial attempt to succeed.
func WaitSuccessfulDial(address string) error {
	const waitDur = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), waitDur)
	lastErr := errors.New("timed out dialing")
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return lastErr
		default:
		}
		var conn net.Conn
		conn, lastErr = net.Dial("tcp", address)
		if lastErr == nil {
			return conn.Close()
		}
	}
}