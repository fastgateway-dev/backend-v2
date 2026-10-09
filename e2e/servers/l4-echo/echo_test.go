//go:build e2e

package main

import (
	"net"
	"testing"
	"time"
)

func TestTCPEchoRoundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go serveTCP(ln)

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))

	want := []byte("ping-tcp")
	if _, err := c.Write(want); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(want))
	if _, err := readFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != string(want) {
		t.Fatalf("got %q want %q", buf, want)
	}
}

func TestUDPEchoRoundTrip(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go serveUDP(pc)

	c, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))

	want := []byte("ping-udp")
	if _, err := c.Write(want); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != string(want) {
		t.Fatalf("got %q want %q", buf[:n], want)
	}
}
