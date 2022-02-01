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

	var lastErr error

	for _, fe := range frontends {
		if err := fe.Start(); err != nil {
			lastErr = err
			p.log.Error(err, "failed to start frontend", "host", fe.BindHost, "port", fe.BindPort)
		}
	}

	p.frontends = frontends

	if lastErr == nil {
		p.log.Info("all frontends running")
		return
	}

	p.log.Info("some frontends failed to start")
}

func (p *L4Proxy) Stop() {
	for _, fe := range p.frontends {
		fe.Stop()
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())

	var configFiles []string
	flag.StringSliceVarP(&configFiles, "config", "c", nil, "configuration files")

	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	flag.Set("v", "1")
	flag.Set("logtostderr", "true")
	flag.Parse()

	if len(configFiles) == 0 {
		fmt.Fprintf(os.Stderr, "no config file provided, exiting.\n")
		os.Exit(1)
	}

	log := glogr.New()

	cfgFileUpdateCh := make(chan string)

	for _, configFile := range configFiles {
		cfgFileLog := log.WithValues("config_file", configFile)
		go func(updateCh chan<- string, configFile string, log logr.Logger) {
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
					updateCh <- configFile
				}
			}
		}(cfgFileUpdateCh, configFile, cfgFileLog)
	}

	go func(cfgFileUpdateCh <-chan string) {
		proxies := make(map[string]*L4Proxy)
		for configFile := range cfgFileUpdateCh {
			cfgFileLog := log.WithValues("config_file", configFile)
			cfgFileLog.V(2).Info("config file update, reloading configuration")
			cfg, err := config.Read(configFile)
			if err != nil {
				cfgFileLog.Error(err, "could not read config file")
				continue
			}
			cfgFileLog.Info("restarting proxy")
			p := proxies[configFile]
			if p != nil {
				p.Stop()
			}
			newProxy := NewL4Proxy(*cfg, cfgFileLog)
			proxies[configFile] = &newProxy
			newProxy.Start()
		}
	}(cfgFileUpdateCh)

	ch := make(chan struct{})
	<-ch
}
