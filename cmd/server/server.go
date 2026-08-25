// Package server runs a layer 4 TCP proxy server.
package server

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/makkes/l4proxy/config"
	"github.com/makkes/l4proxy/server"
)

// NewCommand creates and returns the server subcommand for running the HTTP API server.
func NewCommand(log **slog.Logger) *cobra.Command {
	var configFiles []string

	cmd := &cobra.Command{
		Use:   "server",
		Short: "run the proxy server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), *log, configFiles)
		},
	}

	cmd.Flags().StringArrayVarP(&configFiles, "config", "c", nil, "configuration files")

	return cmd
}

//nolint:gocognit // TODO: reduce cognitive complexity
//revive:disable:cyclomatic // TODO: reduce cognitive complexity
func run(ctx context.Context, log *slog.Logger, configFiles []string) error {
	if len(configFiles) == 0 {
		return errors.New("no config file provided")
	}

	cfgFileUpdateCh := make(chan string)

	go func(cfgFileUpdateCh <-chan string) {
		proxies := make(map[string]*server.L4Proxy)
		for configFile := range cfgFileUpdateCh {
			cfgFileLog := log.With("config_file", configFile)
			cfgFileLog.Info("config file update, reloading configuration")
			cfg, err := config.Read(configFile)
			if err != nil {
				cfgFileLog.Error("could not read config file", "error", err)
				continue
			}
			p := proxies[configFile]
			if p != nil {
				cfgFileLog.Info("restarting proxy")
				p.Stop()
			} else {
				cfgFileLog.Info("starting proxy")
			}
			newProxy := server.NewL4Proxy(*cfg, cfgFileLog)
			proxies[configFile] = &newProxy
			newProxy.Start()
		}
	}(cfgFileUpdateCh)

	for _, configFile := range configFiles {
		cfgFileUpdateCh <- configFile // initial message to start all proxies
		cfgFileLog := log.With("config_file", configFile)
		go func(updateCh chan<- string, configFile string, log *slog.Logger) {
			var lastModTime time.Time
			ticker := time.NewTicker(3 * time.Second)
			for range ticker.C {
				cfgFile, err := os.Stat(configFile)
				if err != nil {
					log.Error("failed to stat configuration file for modification checking", "error", err)
					continue
				}
				if cfgFile.ModTime().After(lastModTime) {
					if !lastModTime.IsZero() {
						updateCh <- configFile
					}
					lastModTime = cfgFile.ModTime()
				}
			}
		}(cfgFileUpdateCh, configFile, cfgFileLog)
	}

	<-ctx.Done()

	return nil
}
