package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/go-logr/glogr"
	"github.com/go-logr/logr"
)

func proxy(log logr.Logger, to net.Conn, from net.Conn, quitChan <-chan struct{}) <-chan struct{} {
	closeChan := make(chan struct{})
	log = log.WithName(fmt.Sprintf("%s->%s", from.RemoteAddr().String(), to.RemoteAddr().String()))
	go func() {
		buf := make([]byte, 1024)
		var nRead int
		for {
			select {
			case <-quitChan:
				return
			default:
				var err error
				nRead, err = from.Read(buf)
				if err != nil {
					opErr, ok := err.(*net.OpError)
					if err == io.EOF || ok && opErr.Err.Error() == "use of closed network connection" {
						log.V(4).Info("connection has been closed", "conn", from.RemoteAddr().String())
					} else {
						log.V(4).Info("error reading from conn", "conn", from.RemoteAddr().String(), "err", err)
					}
					close(closeChan)
					return
				}
				log.V(5).Info("read complete", "bytes", nRead)
			}
			select {
			case <-quitChan:
				return
			default:
				n, err := to.Write(buf[0:nRead])
				if err != nil {
					opErr, ok := err.(*net.OpError)
					if err == io.EOF || ok && opErr.Err.Error() == "use of closed network connection" {
						log.V(4).Info("connection has been closed", "conn", from.RemoteAddr().String())
					} else {
						log.V(4).Info("error writing to conn", "conn", to.RemoteAddr().String(), "err", err)
					}
					close(closeChan)
					return
				}
				log.V(5).Info("write complete", "bytes", n)
			}
		}
	}()
	return closeChan
}

func handleConn(log logr.Logger, cconn net.TCPConn, backend net.TCPAddr) {
	log.V(3).Info("handling incoming connection", "remote", cconn.RemoteAddr().String())
	defer cconn.Close()
	beconn, err := net.DialTCP("tcp4", nil, &backend)
	if err != nil {
		log.Error(err, "error dialing backend")
		return
	}
	defer beconn.Close()

	quitChan := make(chan struct{})
	beDirChan := proxy(log, beconn, &cconn, quitChan)
	clDirChan := proxy(log, &cconn, beconn, quitChan)
	select {
	case <-beDirChan:
		close(quitChan)
		return
	case <-clDirChan:
		close(quitChan)
		return
	}
}

func main() {
	var listenHost string
	flag.StringVar(&listenHost, "host", "127.0.0.1", "the host to listen on")
	var listenPort string
	flag.StringVar(&listenPort, "port", "9999", "the port to listen on")
	var backendHost string
	flag.StringVar(&backendHost, "backend-host", "", "the backend host to forward traffic to")
	var backendPort string
	flag.StringVar(&backendPort, "backend-port", "", "the backend port to forward traffic to")

	flag.Set("v", "1")
	flag.Set("logtostderr", "true")

	flag.Parse()

	log := glogr.New()

	listenAddr, err := net.ResolveTCPAddr("tcp4", fmt.Sprintf("%s:%s", listenHost, listenPort))
	if err != nil {
		log.Error(err, "cannot parse listening address", "host", listenHost, "port", listenPort)
		os.Exit(1)
	}
	ln, err := net.ListenTCP("tcp4", listenAddr)
	if err != nil {
		log.Error(err, "cannot start listener", "addr", listenAddr)
		os.Exit(1)
	}

	backendAddr, err := net.ResolveTCPAddr("tcp4", fmt.Sprintf("%s:%s", backendHost, backendPort))
	if err != nil {
		log.Error(err, "cannot parse backend address", "host", backendHost, "port", backendPort)
		os.Exit(1)
	}

	log.V(1).Info("listener started", "address", listenAddr.String(), "backend", backendAddr.String())

	debug := os.Getenv("DEBUG")
	if debug != "" {
		go func() {
			log := log.WithName("prof")
			for range time.Tick(2 * time.Second) {
				log.Info("profile", "goroutines", runtime.NumGoroutine())
			}
		}()
	}

	for {
		conn, err := ln.AcceptTCP()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error accepting connection: %s", err)
			continue
		}
		go handleConn(log, *conn, *backendAddr)
	}
}
