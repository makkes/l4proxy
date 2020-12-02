package main

import (
	"net"
	"time"

	"github.com/go-logr/logr"
)

func BoolPtr(b bool) *bool {
	return &b
}

type Backend struct {
	Addr    net.TCPAddr `json:"addr"`
	log     logr.Logger
	LastErr error `json:"lastErr"`
	Healthy *bool `json:"healthy"`
	stopCh  chan struct{}
}

func NewBackend(addr net.TCPAddr, log logr.Logger) Backend {
	return Backend{
		Addr: addr,
		log:  log,
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

func (b *Backend) HandleConn(c net.TCPConn) {
	b.log.V(3).Info("handling incoming connection", "remote", c.RemoteAddr().String())
	defer c.Close()
	beconn, err := net.DialTCP("tcp4", nil, &b.Addr)
	if err != nil {
		b.Healthy = BoolPtr(false)
		b.LastErr = err
		b.log.Error(err, "error dialing backend")
		return
	}
	b.Healthy = BoolPtr(true)
	defer beconn.Close()

	quitChan := make(chan struct{})
	beDirChan := proxy(b.log, beconn, &c, quitChan)
	clDirChan := proxy(b.log, &c, beconn, quitChan)
	select {
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
	conn, err := net.DialTCP("tcp4", nil, &b.Addr)
	if err != nil {
		if b.Healthy == nil || *b.Healthy {
			b.log.V(2).Info("backend got unhealthy", "backend", b)
		}
		b.LastErr = err
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
