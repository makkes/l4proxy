// Package server manages layer 4 proxy instances from configuration.
package server

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/makkes/l4proxy/config"
	"github.com/makkes/l4proxy/frontend"
)

// L4Proxy manages the frontends described by an l4proxy configuration.
type L4Proxy struct {
	cfg       config.Config
	log       *slog.Logger
	frontends []*frontend.Frontend
}

// NewL4Proxy creates a proxy from the supplied configuration.
func NewL4Proxy(cfg config.Config, log *slog.Logger) L4Proxy {
	return L4Proxy{
		cfg: cfg,
		log: log,
	}
}

// Start creates and starts all configured frontends.
func (p *L4Proxy) Start() {
	frontends := make([]*frontend.Frontend, 0, len(p.cfg.Frontends))
	for _, feCfg := range p.cfg.Frontends {
		fe, err := frontend.NewFrontend("tcp", feCfg.Bind, p.log,
			frontend.WithTimeout(feCfg.Timeout),
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating frontend: %s\n", err.Error())
			os.Exit(1) //revive:disable:deep-exit // TODO: refactor
		}
		for _, beCfg := range feCfg.Backends {
			if err := fe.AddBackend(beCfg.Address, feCfg.HealthInterval); err != nil {
				p.log.Error("error adding backend", "error", err, "backend", beCfg, "frontend", feCfg)
			}
		}
		frontends = append(frontends, &fe)
	}

	var lastErr error

	for _, fe := range frontends {
		if err := fe.Start(); err != nil {
			lastErr = err
			p.log.Error("failed to start frontend", "error", err, "host", fe.BindHost, "port", fe.BindPort)
		}
	}

	p.frontends = frontends

	if lastErr == nil {
		p.log.Info("all frontends running")
		return
	}

	p.log.Info("some frontends failed to start")
}

// Stop stops all frontends managed by the proxy.
func (p *L4Proxy) Stop() {
	for _, fe := range p.frontends {
		fe.Stop()
	}
}
