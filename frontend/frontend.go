// Package frontend implements the frontend part of the proxy, listening on a host and port and serving one or more backends.
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

// Frontend represents a frontend listening on a host and port and serving one or more backends.
type Frontend struct {
	BindNetwork string
	BindHost    string
	BindPort    string
	Log         logr.Logger
	Backends    []*backend.Backend
	timeout     time.Duration
	listener    net.Listener
}

// Option represents an Option passed to [NewFrontend].
type Option func(f *Frontend)

// WithTimeout sets the timeout options for a [Frontend]. See [NewFrontend].
func WithTimeout(t time.Duration) Option {
	return func(f *Frontend) {
		f.timeout = t
	}
}

const (
	interfacePrefix         = "@"
	defaultKeepaliveTimeout = 30 * time.Second
)

// NewFrontend creates a new frontend with the given configuration. Use [Frontend.Start] for starting the listener.
func NewFrontend(network, bind string, log logr.Logger, opts ...Option) (Frontend, error) {
	var f Frontend
	hostPort, err := parseHostPort(bind)
	if err != nil {
		return f, fmt.Errorf("error parsing frontend bind spec: %w", err)
	}
	f.BindNetwork = network
	f.BindHost = hostPort.Host
	f.BindPort = hostPort.Port
	f.Log = log.WithValues("network", network, "bind", bind)

	for _, opt := range opts {
		opt(&f)
	}

	return f, nil
}

// HostPort represents a host and port tuple for a backend.
type HostPort struct {
	Host string
	Port string
}

func parseHostPort(hp string) (HostPort, error) {
	parts := strings.SplitN(hp, ":", 2)
	if len(parts) == 0 {
		return HostPort{}, fmt.Errorf("wrong format of bind spec '%s'. Expected [host:]port", hp)
	}
	var host, port string
	switch len(parts) {
	case 1:
		host = ""
		port = parts[0]
	case 2:
		if parts[1] == "" {
			return HostPort{}, fmt.Errorf("bind spec '%s' is missing a port", hp)
		}
		if strings.HasPrefix(parts[0], interfacePrefix) {
			var err error
			host, err = hostFromInterface(strings.TrimPrefix(parts[0], interfacePrefix))
			if err != nil {
				return HostPort{}, fmt.Errorf("failed getting IP address from interface: %w", err)
			}
		} else {
			host = parts[0]
		}
		port = parts[1]
	default:
		return HostPort{}, fmt.Errorf("unexpected number of parts in %q. This is a bug that must be fixed", hp)
	}

	return HostPort{Host: host, Port: port}, nil
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

// AddBackend creates a new [backend.Backend] and adds it to the list of backends served by this frontend.
func (f *Frontend) AddBackend(hostPort string, healthInterval int) error {
	backendAddr, err := parseHostPort(hostPort)
	if err != nil {
		return fmt.Errorf("backend spec '%s' has errors: %w", hostPort, err)
	}

	be := backend.NewBackend("tcp4", fmt.Sprintf("%s:%s", backendAddr.Host, backendAddr.Port), f.Log)
	if err := be.Start(healthInterval); err != nil {
		return fmt.Errorf("failed to start backend: %w", err)
	}
	f.Backends = append(f.Backends, be)

	return nil
}

// Start starts the frontend so that connections to it are proxied to/from the configured backends.
// The frontend is shut down by a call to [Frontend.Stop] or by the frontend failing to accept connections
// on the given address.
//
//nolint:gocognit // TODO: refactor this
//revive:disable:cyclomatic // TODO: refactor this
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

	keepaliveTimeout := f.timeout
	if keepaliveTimeout == 0 {
		keepaliveTimeout = defaultKeepaliveTimeout
	}

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

			ctx, cancel := context.WithCancel(context.Background())
			quitCh := make(chan struct{})
			keepaliveChan := make(chan struct{})

			go func(quitCh chan struct{}) {
				handleConn(ctx, f.Log, conn, keepaliveChan, f.Backends)
				close(quitCh)
			}(quitCh)

			go func() {
				timer := time.NewTimer(keepaliveTimeout)
				for {
					select {
					case <-timer.C:
						f.Log.V(5).Info("connection timed out, closing", "conn", conn.RemoteAddr())
						cancel()
						return
					case <-keepaliveChan:
						f.Log.V(5).Info("keeping connection alive", "conn", conn.RemoteAddr())
						if !timer.Stop() {
							<-timer.C
						}
						timer.Reset(keepaliveTimeout)
					}
				}
			}()
		}
	}()

	return nil
}

// Stop stops the frontend's listener as well as all backends. See [backend.Backend.Stop].
func (f *Frontend) Stop() {
	if f.listener != nil {
		if err := f.listener.Close(); err != nil {
			f.Log.Error(err, "failed closing listener connection")
		}
		for _, be := range f.Backends {
			be.Stop()
		}
	}
	f.Log.V(4).Info("frontend stopped")
}

func handleConn(ctx context.Context, log logr.Logger, cconn net.Conn, keepaliveChan chan<- struct{}, backends []*backend.Backend) {
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
			if err := backends[idx].HandleConn(ctx, cconn, keepaliveChan); err != nil {
				log.Error(err, "error handling connection",
					"client", cconn.RemoteAddr().String(),
					"backend_net", backends[idx].Network,
					"backend_addr", backends[idx].Addr)
			}
			return
		}
		log.V(4).Info("skipping unhealthy backend", "backend", backends[idx])
	}
	log.Error(nil, "all backends are unhealthy")
	if err := cconn.Close(); err != nil {
		log.Error(err, "failed closing client connection")
	}
}
