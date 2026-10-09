//go:build e2e

package main

import (
	"log"
	"net"
	"os"
)

func main() {
	tcpPort := envOr("TCP_PORT", "9100")
	udpPort := envOr("UDP_PORT", "9101")

	ln, err := net.Listen("tcp", ":"+tcpPort)
	if err != nil {
		log.Fatalf("tcp listen: %v", err)
	}
	pc, err := net.ListenPacket("udp", ":"+udpPort)
	if err != nil {
		log.Fatalf("udp listen: %v", err)
	}
	log.Printf("l4-echo listening tcp :%s udp :%s", tcpPort, udpPort)

	go serveUDP(pc)
	serveTCP(ln)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
