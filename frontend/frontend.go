package frontend

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/makkes/l4proxy/backend"
)

type Frontend struct {
	BindNetwork string
	BindHost    string
	BindPort    string
	Log         logr.Logger
	Backends    []*backend.Backend
	listener    net.Listener
}

type Option func(f *Frontend)

func NewFrontend(network, bind string, log logr.Logger, opts ...Option) (Frontend, error) {
	var f Frontend
	bindHost, bindPort, err := parseHostPort(bind)
	if err != nil {
		return f, fmt.Errorf("error parsing frontend bind spec: %w", err)
	}
	f.BindNetwork = network
	f.BindHost = bindHost
	f.BindPort = bindPort
	f.Log = log

	for _, opt := range opts {
		opt(&f)
	}

	return f, nil
}

func parseHostPort(hp string) (string, string, error) {
	parts := strings.SplitN(hp, ":", 2)
	if len(parts) == 0 {
		return "", "", fmt.Errorf("wrong format of bind spec '%s'. Expected [host:]port", hp)
	}
	var host, port string
	switch len(parts) {
	case 1:
		host = ""
		port = parts[0]
	case 2:
		if parts[1] == "" {
			return "", "", fmt.Errorf("bind spec '%s' is missing a port", hp)
		} else {
			host = parts[0]
			port = parts[1]
		}
	}

	return host, port, nil
}

func (f *Frontend) AddBackend(be string, healthInterval int) error {
	host, port, err := parseHostPort(be)
	if err != nil {
		return fmt.Errorf("backend spec '%s' has errors: %w", be, err)
	}

	backend := backend.NewBackend("tcp4", fmt.Sprintf("%s:%s", host, port), f.Log)
	backend.Start(healthInterval)
	f.Backends = append(f.Backends, backend)

	return nil
}

func (f *Frontend) Start() {
	listenAddr, err := net.ResolveTCPAddr("tcp4", fmt.Sprintf("%s:%s", f.BindHost, f.BindPort))
	if err != nil {
		f.Log.Error(err, "cannot parse listening address", "host", f.BindHost, "port", f.BindPort)
		return
	}
	f.listener, err = net.ListenTCP("tcp4", listenAddr)
	if err != nil {
		f.Log.Error(err, "cannot start listener", "addr", listenAddr)
		return
	}

	go func() {
		for {
			conn, err := f.listener.Accept()
			if err != nil {
				f.Log.Error(err, "Error accepting connection")
				return
			}
			ctx, _ := context.WithTimeout(context.Background(), 30*time.Second) // nolint:govet // this is an endless loop
			go handleConn(ctx, f.Log, conn, f.Backends)
		}
	}()
}

func (f *Frontend) Stop() {
	if f.listener != nil {
		f.listener.Close()
		for _, be := range f.Backends {
			be.Stop()
		}
	}
}

func handleConn(ctx context.Context, log logr.Logger, cconn net.Conn, backends []*backend.Backend) {
	idcs := make([]int, len(backends))
	for idx := range backends {
		idcs[idx] = idx
	}
	rand.Shuffle(len(idcs), func(i, j int) {
		idcs[i], idcs[j] = idcs[j], idcs[i]
	})
	for _, idx := range idcs {
		if backends[idx].IsHealthy() {
			log.V(4).Info("selecting backend", "backend", backends[idx])
			if err := backends[idx].HandleConn(ctx, cconn); err != nil {
				log.Error(err, "error handling connection", "client", cconn.RemoteAddr().String(), "backend_net", "backend_addr", backends[idx].Network, backends[idx].Addr)
			}
			return
		}
		log.V(4).Info("skipping unhealthy backend", "backend", backends[idx])
	}
	log.Error(nil, "all backends are unhealthy")
	cconn.Close()
}
