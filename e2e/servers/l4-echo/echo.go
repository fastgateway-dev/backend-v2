//go:build e2e

package main

import (
	"io"
	"net"
)

// serveTCP accepts connections and echoes each one back to the peer until the
// peer closes the connection.
func serveTCP(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			defer conn.Close()
			io.Copy(conn, conn)
		}(c)
	}
}

// serveUDP echoes every received datagram back to its sender.
func serveUDP(pc net.PacketConn) {
	buf := make([]byte, 65535)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		pc.WriteTo(buf[:n], addr)
	}
}

func readFull(c net.Conn, buf []byte) (int, error) {
	return io.ReadFull(c, buf)
}
