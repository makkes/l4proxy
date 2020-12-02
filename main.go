package main

import (
	"context"
	goflag "flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/go-logr/glogr"
	"github.com/go-logr/logr"
	flag "github.com/spf13/pflag"

	"github.com/makkes/l4proxy/backend"
)

func handleConn(ctx context.Context, log logr.Logger, cconn net.TCPConn, backends []*backend.Backend) {
	idcs := make([]int, len(backends))
	for idx := range backends {
		idcs[idx] = idx
	}
	rand.Shuffle(len(idcs), func(i, j int) {
		idcs[i], idcs[j] = idcs[j], idcs[i]
	})
	for _, idx := range idcs {
		if backends[idx].Healthy != nil && *backends[idx].Healthy {
			log.V(4).Info("selecting backend", "backend", backends[idx])
			backends[idx].HandleConn(ctx, &cconn)
			return
		}
		log.V(4).Info("skipping unhealthy backend", "backend", backends[idx])
	}
	log.Error(nil, "all backends are unhealthy")
	cconn.Close()
}

func main() {
	rand.Seed(time.Now().UnixNano())

	var listenHost string
	flag.StringVar(&listenHost, "host", "127.0.0.1", "the host to listen on")
	var listenPort string
	flag.StringVar(&listenPort, "port", "9999", "the port to listen on")
	var backendsFlag []string
	flag.StringSliceVar(&backendsFlag, "backends", nil, "comma-separated list of backend(s) to forward traffic to. Format [host:]port")
	var healthInterval int
	flag.IntVar(&healthInterval, "health-interval", 15, "how often (in seconds) to check for each backend's health")

	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
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

	backends := make([]*backend.Backend, 0)
	for _, be := range backendsFlag {
		parts := strings.SplitN(be, ":", 2)
		if len(parts) == 0 {
			log.Error(nil, "wrong format of backend spec", "spec", be)
			os.Exit(1)
		}
		var host, port string
		switch len(parts) {
		case 1:
			host = ""
			port = parts[0]
		case 2:
			if parts[1] == "" {
				log.Error(nil, "backend spec is missing a port", "spec", be)
				os.Exit(1)
			} else {
				host = parts[0]
				port = parts[1]
			}
		}

		backend := backend.NewBackend("tcp4", fmt.Sprintf("%s:%s", host, port), log)
		backend.Start(healthInterval)
		backends = append(backends, &backend)
	}

	log.V(1).Info("listener started", "address", listenAddr.String(), "backends", backends)

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
		ctx, _ := context.WithTimeout(context.Background(), 30*time.Second) // nolint:govet
		go handleConn(ctx, log, *conn, backends)
	}
}
