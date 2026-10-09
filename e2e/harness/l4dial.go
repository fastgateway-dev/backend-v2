//go:build e2e

package harness

import (
	"context"
	"fmt"
	"net"
	"time"
)

// DialTCP opens a TCP connection to addr, writes payload, and reads back
// len(payload) bytes (the echo). It retries the whole connect+write+read until
// the timeout deadline, because on a cold cluster the Stream Gateway's LB port
// is plumbed a few seconds after the route deploys and the first connects are
// refused — treating timeout as the overall deadline (not a single attempt)
// is what keeps the TCP traffic tests from flaking on connection-refused.
func DialTCP(ctx context.Context, addr string, payload []byte, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		got, err := dialTCPOnce(ctx, addr, payload)
		if err == nil {
			return got, nil
		}
		lastErr = err
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("dial tcp %s: no success before deadline: %w", addr, lastErr)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func dialTCPOnce(ctx context.Context, addr string, payload []byte) ([]byte, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write(payload); err != nil {
		return nil, err
	}
	buf := make([]byte, len(payload))
	n := 0
	for n < len(payload) {
		m, err := c.Read(buf[n:])
		if err != nil {
			return nil, err
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
