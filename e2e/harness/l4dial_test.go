//go:build e2e

package harness

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestDialTCPEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) { io.Copy(conn, conn); conn.Close() }(c)
		}
	}()

	got, err := DialTCP(context.Background(), ln.Addr().String(), []byte("hi-tcp"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi-tcp" {
		t.Fatalf("got %q want %q", got, "hi-tcp")
	}
}

func TestDialUDPEcho(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		b := make([]byte, 1500)
		for {
			n, a, err := pc.ReadFrom(b)
			if err != nil {
				return
			}
			pc.WriteTo(b[:n], a)
		}
	}()

	got, err := DialUDP(context.Background(), pc.LocalAddr().String(), []byte("hi-udp"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi-udp" {
		t.Fatalf("got %q want %q", got, "hi-udp")
	}
}

func TestDialUDPNoServerFailsFast(t *testing.T) {
	// A closed/unused UDP port: no echo ever arrives, so DialUDP must return
	// an error by the deadline rather than hang.
	start := time.Now()
	_, err := DialUDP(context.Background(), "127.0.0.1:1", []byte("x"), 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for unreachable UDP echo")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("DialUDP took too long: %v", time.Since(start))
	}
}
