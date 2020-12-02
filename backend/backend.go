package backend

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/go-logr/logr"
)

type proxyFunc func(log logr.Logger, to net.Conn, from net.Conn, quitChan <-chan struct{}) <-chan struct{}

func BoolPtr(b bool) *bool {
	return &b
}

type Backend struct {
	Addr    string `json:"addr"`
	Network string `json:"network"`
	log     logr.Logger
	LastErr error `json:"lastErr"`
	Healthy *bool `json:"healthy"`
	stopCh  chan struct{}
	proxy   proxyFunc
}

func proxy(log logr.Logger, to net.Conn, from net.Conn, quitChan <-chan struct{}) <-chan struct{} {
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
		}
	}()
	return closeChan
}

type Option func(b *Backend)

func NewBackend(network string, addr string, log logr.Logger, opts ...Option) Backend {
	b := Backend{
		Addr:    addr,
		Network: network,
		log:     log,
		proxy:   proxy,
	}

	for _, opt := range opts {
		opt(&b)
	}

	return b
}

func WithProxyFunc(f proxyFunc) Option {
	return func(b *Backend) {
		b.proxy = f
	}
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
				b.stopCh = nil
				return
			case <-ticker.C:
				b.checkHealth()
			}
		}
	}()
}

func (b Backend) Stop() {
	if b.stopCh == nil {
		// not running
		return
	}
	close(b.stopCh)
}

func (b *Backend) HandleConn(ctx context.Context, c net.Conn) {
	b.log.V(3).Info("handling incoming connection", "remote", c.RemoteAddr().String())
	defer c.Close()
	var dialer net.Dialer
	beconn, err := dialer.DialContext(ctx, b.Network, b.Addr)
	if err != nil {
		b.Healthy = BoolPtr(false)
		b.LastErr = err
		b.log.Error(err, "error dialing backend")
		return
	}
	b.Healthy = BoolPtr(true)

	quitChan := make(chan struct{})
	beDirChan := b.proxy(b.log, beconn, c, quitChan)
	clDirChan := b.proxy(b.log, c, beconn, quitChan)

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
		return
	case <-beDirChan:
		close(quitChan)
		return
	case <-clDirChan:
		close(quitChan)
		return
	}
}

func (b *Backend) checkHealth() {
	b.log.V(5).Info("checking health", "backend", b)
	conn, err := net.Dial(b.Network, b.Addr)
	if err != nil {
		b.LastErr = err
		if b.Healthy == nil || *b.Healthy {
			b.log.V(2).Info("backend got unhealthy", "backend", b)
		}
		b.Healthy = BoolPtr(false)
		return
	}
	if err := conn.Close(); err != nil {
		b.LastErr = err
		b.Healthy = BoolPtr(false)
	}
	if b.Healthy == nil || !*b.Healthy {
		b.log.V(2).Info("backend got healthy", "backend", b.Addr)
	}
	b.Healthy = BoolPtr(true)
}
