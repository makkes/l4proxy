package backend

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

type proxyFunc func(log logr.Logger, to net.Conn, from net.Conn, quitChan <-chan struct{}, keepaliveChan chan<- struct{}) <-chan struct{}

func BoolPtr(b bool) *bool {
	return &b
}

type Backend struct {
	Addr    string `json:"addr"`
	Network string `json:"network"`
	log     logr.Logger
	LastErr error `json:"lastErr"`
	healthy *bool
	stopCh  chan struct{}
	proxy   proxyFunc
	mux     sync.RWMutex
}

func proxy(log logr.Logger, to net.Conn, from net.Conn, quitChan <-chan struct{}, keepaliveChan chan<- struct{}) <-chan struct{} {
	closeChan := make(chan struct{})
	log = log.WithName(fmt.Sprintf("%s->%s", from.RemoteAddr().String(), to.RemoteAddr().String()))
	go func() {
		buf := make([]byte, 1024)
		var nRead int
		for {
			select {
			case <-quitChan:
				close(closeChan)
				return
			default:
				var err error
				nRead, err = from.Read(buf)
				if err != nil {
					opErr, ok := err.(*net.OpError)
					if err == io.EOF || ok && opErr.Err.Error() == "use of closed network connection" {
						log.V(4).Info("connection has been closed", "conn", from.RemoteAddr().String())
					} else {
						log.V(4).Info("error reading from conn", "conn", from.RemoteAddr().String(), "err", err.Error())
					}
					close(closeChan)
					return
				}
				log.V(5).Info("read complete", "bytes", nRead)
			}
			select {
			case <-quitChan:
				close(closeChan)
				return
			default:
				log.V(6).Info(fmt.Sprintf("writing to %s", to.RemoteAddr().String()))
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
			keepaliveChan <- struct{}{}
		}
	}()
	return closeChan
}

type Option func(b *Backend)

func NewBackend(network string, addr string, log logr.Logger, opts ...Option) *Backend {
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

func WithProxyFunc(f proxyFunc) Option {
	return func(b *Backend) {
		b.proxy = f
	}
}

func (b *Backend) IsHealthy() bool {
	b.mux.RLock()
	healthy := b.healthy != nil && *b.healthy
	b.mux.RUnlock()
	return healthy
}

func (b *Backend) Start(interval int) {
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
}

func (b *Backend) Stop() {
	if b.stopCh == nil {
		// not running
		return
	}
	close(b.stopCh)
}

func (b *Backend) HandleConn(ctx context.Context, c net.Conn, keepaliveChan chan<- struct{}) error {
	b.log.V(3).Info("handling incoming connection", "remote", c.RemoteAddr().String())
	defer c.Close()
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
		beconn.Close()
		c.Close()
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
	b.healthy = BoolPtr(healthy)
	b.LastErr = err
	b.mux.Unlock()
}

func (b *Backend) checkHealth() {
	b.log.V(5).Info("checking health", "backend", b)
	conn, err := net.Dial(b.Network, b.Addr)
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
