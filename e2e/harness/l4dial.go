//go:build e2e

package harness

import (
	"context"
	"fmt"
	"net"
	"time"
)

// DialTCP opens a TCP connection to addr, writes payload, and reads back
// len(payload) bytes (the echo), bounded by timeout.
func DialTCP(ctx context.Context, addr string, payload []byte, timeout time.Duration) ([]byte, error) {
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial tcp %s: %w", addr, err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	if _, err := c.Write(payload); err != nil {
		return nil, fmt.Errorf("write tcp %s: %w", addr, err)
	}
	buf := make([]byte, len(payload))
	n := 0
	for n < len(payload) {
		m, err := c.Read(buf[n:])
		if err != nil {
			return nil, fmt.Errorf("read tcp %s: %w", addr, err)
		}
		n += m
	}
	return buf, nil
}

// DialUDP sends payload to addr and waits for an echoed datagram, retrying
// until timeout because UDP is lossy. Success is any echo received.
func DialUDP(ctx context.Context, addr string, payload []byte, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c, err := net.Dial("udp", addr)
		if err != nil {
			return nil, fmt.Errorf("dial udp %s: %w", addr, err)
		}
		c.SetDeadline(time.Now().Add(500 * time.Millisecond))
		if _, err := c.Write(payload); err != nil {
			c.Close()
			lastErr = err
			continue
		}
		buf := make([]byte, len(payload)+64)
		n, err := c.Read(buf)
		c.Close()
		if err == nil {
			return buf[:n], nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("udp echo %s: no reply before deadline: %w", addr, lastErr)
}
