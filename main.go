package main

import (
	goflag "flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/go-logr/glogr"
	"github.com/go-logr/logr"
	flag "github.com/spf13/pflag"

	"github.com/makkes/l4proxy/config"
	"github.com/makkes/l4proxy/frontend"
)

type L4Proxy struct {
	cfg       config.Config
	log       logr.Logger
	frontends []*frontend.Frontend
}

func NewL4Proxy(cfg config.Config, log logr.Logger) L4Proxy {
	return L4Proxy{
		cfg: cfg,
		log: log,
	}
}

func (p *L4Proxy) Start() {
	frontends := make([]*frontend.Frontend, 0)
	for _, feCfg := range p.cfg.Frontends {
		fe, err := frontend.NewFrontend("tcp", feCfg.Bind, p.log)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating frontend: %s\n", err.Error())
			os.Exit(1)
		}
		for _, beCfg := range feCfg.Backends {
			if err := fe.AddBackend(beCfg.Address, feCfg.HealthInterval); err != nil {
				fmt.Fprintf(os.Stderr, "error adding backend '%s': %s\n", beCfg.Address, err.Error())
				os.Exit(1)
			}
		}
		frontends = append(frontends, &fe)
	}

	// go startWebServer(log, cfg)

	for _, fe := range frontends {
		fe.Start()
	}

	p.frontends = frontends

	p.log.V(5).Info("all frontends running")
}

func (p *L4Proxy) Stop() {
	for _, fe := range p.frontends {
		fe.Stop()
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())

	var configFile string
	flag.StringVarP(&configFile, "config", "c", "", "configuration file ")

	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	flag.Set("v", "1")
	flag.Set("logtostderr", "true")
	flag.Parse()

	if configFile == "" {
		fmt.Fprintf(os.Stderr, "no config file provided, exiting.\n")
		os.Exit(1)
	}

	log := glogr.New()

	cfgFileUpdateCh := make(chan struct{})

	go func(updateCh chan<- struct{}) {
		var lastModTime time.Time
		ticker := time.NewTicker(3 * time.Second)
		for range ticker.C {
			cfgFile, err := os.Stat(configFile)
			if err != nil {
				log.Error(err, "failed to stat configuration file for modification checking")
				continue
			}
			if cfgFile.ModTime().After(lastModTime) {
				lastModTime = cfgFile.ModTime()
				updateCh <- struct{}{}
			}
		}
	}(cfgFileUpdateCh)

	go func(cfgFileUpdateCh <-chan struct{}) {
		var proxy L4Proxy
		for range cfgFileUpdateCh {
			log.V(2).Info("config file update, reloading configuration")
			cfg, err := config.Read(configFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not read config file: %s\n", err.Error())
				continue
			}
			log.Info("stopping proxy")
			proxy.Stop()
			proxy = NewL4Proxy(*cfg, log)
			proxy.Start()
		}
	}(cfgFileUpdateCh)

	ch := make(chan struct{})
	<-ch
}
