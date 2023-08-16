package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/miekg/dns"
)

var (
	dnsServerAddress       string
	tlsClientHelloTimeout  time.Duration
	upstreamConnectTimeout time.Duration
)

func main() {
	flag.StringVar(&dnsServerAddress, "dns-server", "114.114.114.114:53", "Address of the DNS server")
	flag.DurationVar(&tlsClientHelloTimeout, "tls-client-hello-timeout", 5*time.Second, "TLS client hello timeout")
	flag.DurationVar(&upstreamConnectTimeout, "upstream-connect-timeout", 5*time.Second, "Upstream connect timeout")

	flag.Parse()

	log.Println("Starting server with configurations:")
	log.Printf("DNS Server Address: %s", dnsServerAddress)
	log.Printf("TLS Client Hello Timeout: %v", tlsClientHelloTimeout)
	log.Printf("Upstream Connect Timeout: %v", upstreamConnectTimeout)

	l, err := net.Listen("tcp", ":443")
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	log.Printf("Listening on %s", l.Addr().String())
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Print(err)
			continue
		}
		go handleConnection(conn)
	}
}

type readOnlyConn struct {
	reader io.Reader
}

func (conn readOnlyConn) Read(p []byte) (int, error) { return conn.reader.Read(p) }
func (conn readOnlyConn) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("readOnlyConn does not support write operations")
}
func (conn readOnlyConn) Close() error                       { return nil }
func (conn readOnlyConn) LocalAddr() net.Addr                { return nil }
func (conn readOnlyConn) RemoteAddr() net.Addr               { return nil }
func (conn readOnlyConn) SetDeadline(t time.Time) error      { return nil }
func (conn readOnlyConn) SetReadDeadline(t time.Time) error  { return nil }
func (conn readOnlyConn) SetWriteDeadline(t time.Time) error { return nil }

func peekClientHello(reader io.Reader) (*tls.ClientHelloInfo, io.Reader, error) {
	peekedBytes := new(bytes.Buffer)
	hello, err := readClientHello(io.TeeReader(reader, peekedBytes))
	if err != nil {
		return nil, nil, err
	}
	return hello, io.MultiReader(peekedBytes, reader), nil
}

func readClientHello(reader io.Reader) (*tls.ClientHelloInfo, error) {
	var hello *tls.ClientHelloInfo

	err := tls.Server(readOnlyConn{reader: reader}, &tls.Config{
		GetConfigForClient: func(argHello *tls.ClientHelloInfo) (*tls.Config, error) {
			hello = new(tls.ClientHelloInfo)
			*hello = *argHello
			return nil, nil
		},
	}).Handshake()

	if hello == nil {
		return nil, err
	}

	return hello, nil
}

func resolveServerNameToIP(serverName string) (string, error) {
	c := dns.Client{}
	m := dns.Msg{}

	m.SetQuestion(dns.Fqdn(serverName), dns.TypeA)
	r, _, err := c.Exchange(&m, dnsServerAddress)
	if err != nil {
		return "", err
	}

	for _, ans := range r.Answer {
		if a, ok := ans.(*dns.A); ok {
			return a.A.String(), nil
		}
	}

	return "", fmt.Errorf("unknown host %s", serverName)
}

func handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	log.Printf("Received connection from %s", clientConn.RemoteAddr().String())

	if err := clientConn.SetReadDeadline(time.Now().Add(tlsClientHelloTimeout)); err != nil {
		log.Print(err)
		return
	}

	clientHello, clientReader, err := peekClientHello(clientConn)
	if err != nil {
		log.Printf("Error peeking client hello from %s: %v", clientConn.RemoteAddr().String(), err)
		return
	}

	if err := clientConn.SetReadDeadline(time.Time{}); err != nil {
		log.Print(err)
		return
	}

	ip, err := resolveServerNameToIP(clientHello.ServerName)
	if err != nil {
		log.Printf("Failed to resolve server name %s from connection %s: %v", clientHello.ServerName, clientConn.RemoteAddr().String(), err)
		return
	}

	log.Printf("Proxying requests for domain %s (resolved as %s) from client %s", clientHello.ServerName, ip, clientConn.RemoteAddr().String())

	backendConn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, "443"), upstreamConnectTimeout)
	if err != nil {
		log.Printf("Error connecting to backend for %s: %v", clientHello.ServerName, err)
		return
	}
	defer backendConn.Close()

	log.Printf("Successfully connected to backend %s for client %s", backendConn.RemoteAddr().String(), clientConn.RemoteAddr().String())

	proxyTraffic(clientConn, backendConn, clientReader)
}

func proxyTraffic(clientConn, backendConn net.Conn, clientReader io.Reader) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		_, err := io.Copy(clientConn, backendConn)
		if err != nil {
			log.Printf("Error copying data from backend to client: %v", err)
		}
		clientConn.(*net.TCPConn).CloseWrite()
		wg.Done()
	}()
	go func() {
		_, err := io.Copy(backendConn, clientReader)
		if err != nil {
			log.Printf("Error copying data from client to backend: %v", err)
		}
		backendConn.(*net.TCPConn).CloseWrite()
		wg.Done()
	}()

	wg.Wait()
	log.Printf("Finished proxying for client %s to backend %s", clientConn.RemoteAddr().String(), backendConn.RemoteAddr().String())
}
