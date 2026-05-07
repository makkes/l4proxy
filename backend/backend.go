// Package backend implements the backend part of the proxy, serving traffic between a frontend and a backend application.
package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

type proxyFunc func(log logr.Logger, to net.Conn, from net.Conn, quitChan <-chan struct{}, keepaliveChan chan<- struct{}) <-chan struct{}

// Backend represents a single backend served by a [frontend.Frontend].
type Backend struct {
	Addr    string `json:"addr"`
	Network string `json:"network"`
	log     logr.Logger
	LastErr error `json:"last_err"`
	healthy *bool
	stopCh  chan struct{}
	proxy   proxyFunc
	mux     sync.RWMutex
}

func isClosedConnErr(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)
}

func logConnErr(log logr.Logger, err error, closedAddr, errAddr, errMsg string) {
	if isClosedConnErr(err) {
		log.V(4).Info("connection has been closed", "conn", closedAddr)
	} else {
		log.V(4).Info(errMsg, "conn", errAddr, "err", err.Error())
	}
}

func quitRequested(quitChan <-chan struct{}) bool {
	select {
	case <-quitChan:
		return true
	default:
		return false
	}
}

func proxy(log logr.Logger, to, from net.Conn, quitChan <-chan struct{}, keepaliveChan chan<- struct{}) <-chan struct{} {
	closeChan := make(chan struct{})
	log = log.WithName(fmt.Sprintf("%s->%s", from.RemoteAddr().String(), to.RemoteAddr().String()))
	go func() {
		defer close(closeChan)
		buf := make([]byte, 1024)
		for {
			if quitRequested(quitChan) {
				return
			}
			nRead, err := from.Read(buf)
			if err != nil {
				logConnErr(log, err, from.RemoteAddr().String(), from.RemoteAddr().String(), "error reading from conn")
				return
			}
			log.V(5).Info("read complete", "bytes", nRead)

			if quitRequested(quitChan) {
				return
			}
			log.V(6).Info("writing to " + to.RemoteAddr().String())
			n, err := to.Write(buf[0:nRead])
			if err != nil {
				logConnErr(log, err, from.RemoteAddr().String(), to.RemoteAddr().String(), "error writing to conn")
				return
			}
			log.V(5).Info("write complete", "bytes", n)

			keepaliveChan <- struct{}{}
		}
	}()
	return closeChan
}

// Option represents a configuration option passed to [NewBackend].
type Option func(b *Backend)

// NewBackend creates a new backend with the given configuration. Use [Backend.Start] to actually start the backend and
// serve traffic.
func NewBackend(network, addr string, log logr.Logger, opts ...Option) *Backend {
	b := &Backend{
		Addr:    addr,
		Network: network,
		log:     log,
		proxy:   proxy,
	}

	for _, opt := range opts {
		opt(b)
	}

	return b
}

// WithProxyFunc overrides the default proxy function. This is useful mainly for testing.
func WithProxyFunc(f proxyFunc) Option {
	return func(b *Backend) {
		b.proxy = f
	}
}

// IsHealthy reports whether the last health check for this backend returned success or not.
// The backend may become unhealthy between health checks so frontend should prepare for a
// non-responsive backend even when IsHealthy reports success.
func (b *Backend) IsHealthy() bool {
	b.mux.RLock()
	healthy := b.healthy != nil && *b.healthy
	b.mux.RUnlock()
	return healthy
}

// Start starts the health check for this backend. Frontend can use [Backend.IsHealthy] to include or exclude this
// backend from serving traffic.
func (b *Backend) Start(interval int) error {
	if interval <= 0 {
		return errors.New("interval must be > 0")
	}
	b.stopCh = make(chan struct{})
	go func() {
		b.checkHealth()
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		for {
			select {
			case <-b.stopCh:
				ticker.Stop()
				return
			case <-ticker.C:
				b.checkHealth()
			}
		}
	}()

	return nil
}

// Stop stops the health check and marks the backend as stopped.
// TODO: It's still possible to call [Backend.HandleConn] after Stop has been called.
func (b *Backend) Stop() {
	if b.stopCh == nil {
		// not running
		return
	}
	close(b.stopCh)
}

// HandleConn starts proxying data between a client represented by the provided net.Conn and this backend.
func (b *Backend) HandleConn(ctx context.Context, c net.Conn, keepaliveChan chan<- struct{}) error {
	b.log.V(3).Info("handling incoming connection", "remote", c.RemoteAddr().String())
	defer func() {
		// make sure that the client connection is closed. It might have already
		// been closed before so we check for net.ErrClosed.
		if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			b.log.Error(err, "failed closing client connection")
		}
	}()
	var dialer net.Dialer
	beconn, err := dialer.DialContext(ctx, b.Network, b.Addr)
	if err != nil {
		b.setHealth(false, err)
		return fmt.Errorf("error dialing backend %s %s: %w", b.Network, b.Addr, err)
	}

	quitChan := make(chan struct{})
	beDirChan := b.proxy(b.log, beconn, c, quitChan, keepaliveChan)
	clDirChan := b.proxy(b.log, c, beconn, quitChan, keepaliveChan)

	defer func() {
		// close connections and wait for goroutines to shut down
		if err := beconn.Close(); err != nil {
			b.log.Error(err, "failed closing backend connection")
		}
		if err := c.Close(); err != nil {
			b.log.Error(err, "failed closing client connection after handling proxy requests")
		}
		<-clDirChan
		<-beDirChan
	}()

	select {
	case <-ctx.Done():
		close(quitChan)
		return nil
	case <-beDirChan:
		close(quitChan)
		return nil
	case <-clDirChan:
		close(quitChan)
		return nil
	}
}

func (b *Backend) setHealth(healthy bool, err error) {
	b.mux.Lock()
	b.healthy = new(healthy)
	b.LastErr = err
	b.mux.Unlock()
}

func (b *Backend) checkHealth() {
	b.log.V(5).Info("checking health", "backend", b)
	var dialer net.Dialer
	conn, err := dialer.DialContext(context.Background(), b.Network, b.Addr) // TODO: use an actual context here.
	if err != nil {
		if b.healthy == nil || *b.healthy {
			b.log.V(2).Info("backend got unhealthy", "backend", b)
		}
		b.setHealth(false, err)

		return
	}
	if err := conn.Close(); err != nil {
		b.setHealth(false, err)
	}
	if b.healthy == nil || !*b.healthy {
		b.log.V(2).Info("backend got healthy", "backend", b.Addr)
	}
	b.setHealth(true, nil)
}
