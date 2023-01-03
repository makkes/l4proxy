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

const (
	interfacePrefix = "@"
)

func NewFrontend(network, bind string, log logr.Logger, opts ...Option) (Frontend, error) {
	var f Frontend
	bindHost, bindPort, err := parseHostPort(bind)
	if err != nil {
		return f, fmt.Errorf("error parsing frontend bind spec: %w", err)
	}
	f.BindNetwork = network
	f.BindHost = bindHost
	f.BindPort = bindPort
	f.Log = log.WithValues("network", network, "bind", bind)

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
			if strings.HasPrefix(parts[0], interfacePrefix) {
				var err error
				host, err = hostFromInterface(strings.TrimPrefix(parts[0], interfacePrefix))
				if err != nil {
					return "", "", fmt.Errorf("failed getting IP address from interface: %w", err)
				}
			} else {
				host = parts[0]
			}
			port = parts[1]
		}
	}

	return host, port, nil
}

func hostFromInterface(ifName string) (string, error) {
	inf, err := net.InterfaceByName(ifName)
	if err != nil {
		return "", fmt.Errorf("failed getting interface by name %q: %w", ifName, err)
	}
	addrs, err := inf.Addrs()
	if err != nil {
		return "", fmt.Errorf("failed getting addresses of interface %q: %w", inf.Name, err)
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("interface %q has no address", inf.Name)
	}

	ipNet, ok := addrs[0].(*net.IPNet)
	if !ok {
		return "", fmt.Errorf("address %q is not an IP network address", addrs[0].String())
	}
	return ipNet.IP.String(), nil
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

func (f *Frontend) Start() error {
	listenAddr, err := net.ResolveTCPAddr("tcp4", fmt.Sprintf("%s:%s", f.BindHost, f.BindPort))
	if err != nil {
		return fmt.Errorf("cannot parse listening address %s:%s: %w", f.BindHost, f.BindPort, err)
	}
	f.listener, err = net.ListenTCP("tcp4", listenAddr)
	if err != nil {
		return fmt.Errorf("cannot start listener at %s: %w", listenAddr, err)
	}
	f.Log.V(4).Info("listener started")

	go func() {
		for {
			conn, err := f.listener.Accept()
			if err != nil {
				if strings.Contains(err.Error(), "use of closed network connection") {
					return // assume this is a legit action caused by calling "Close" on the Frontend.
				}
				f.Log.Error(err, "Error accepting connection", "err", fmt.Sprintf("%#v", err))
				return
			}
			ctx, _ := context.WithTimeout(context.Background(), 30*time.Second) //nolint:govet // this is an endless loop
			go handleConn(ctx, f.Log, conn, f.Backends)
		}
	}()

	return nil
}

func (f *Frontend) Stop() {
	if f.listener != nil {
		f.listener.Close()
		for _, be := range f.Backends {
			be.Stop()
		}
	}
	f.Log.V(4).Info("frontend stopped")
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
