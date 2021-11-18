package main

import (
	"encoding/json"
	goflag "flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/go-logr/glogr"
	"github.com/go-logr/logr"
	flag "github.com/spf13/pflag"

	"github.com/makkes/l4proxy/config"
	"github.com/makkes/l4proxy/frontend"
)

func startWebServer(log logr.Logger) {
	http.HandleFunc("/backends", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				log.V(4).Info("could not unmarshal payload", "err", err.Error())
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, "%s\n", err.Error())
				return
			}
			// fmt.Printf("%#v\n", body)
		}
	})
	http.ListenAndServe("127.0.0.1:1313", nil)
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

	cfg, err := config.Read(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not read config file: %s\n", err.Error())
		os.Exit(1)
	}

	log := glogr.New()

	debug := os.Getenv("DEBUG")
	if debug != "" {
		go func() {
			log := log.WithName("prof")
			for range time.Tick(2 * time.Second) {
				log.Info("profile", "goroutines", runtime.NumGoroutine())
			}
		}()
	}

	go startWebServer(log)

	frontends := make([]frontend.Frontend, 0)
	for _, feCfg := range cfg.Frontends {
		fe, err := frontend.NewFrontend("tcp", feCfg.Bind, log)
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
		frontends = append(frontends, fe)
	}

	var wg sync.WaitGroup

	for idx := range frontends {
		wg.Add(1)
		go func(idx int) {
			frontends[idx].Start()
			wg.Done()
		}(idx)
	}

	log.Info("all frontends running", "frontends", len(frontends))

	wg.Wait()

	log.Info("all frontends quit")
}
