// Package cmd provides the root command and initialization logic for the nominations API CLI.
package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/makkes/l4proxy/cmd/announce"
	"github.com/makkes/l4proxy/cmd/server"
)

// NewRootCommand creates and returns the root command for the CLI.
func NewRootCommand() *cobra.Command {
	var log *slog.Logger
	var logLevel string
	var logFormat string

	cmd := &cobra.Command{
		SilenceUsage: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			var err error
			log, err = initLogger(logLevel, logFormat)
			return err
		},
	}

	hf := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, s []string) {
		hf(c, s)
		os.Exit(1) //revive:disable:deep-exit // We want the help func to exit with 1.
	})

	cmd.PersistentFlags().StringVar(&logLevel, "log-level", "INFO", "log level of the application (DEBUG|INFO|WARN|ERROR)")
	cmd.PersistentFlags().StringVar(&logFormat, "log-format", "json", "format to use for logging (json|text)")

	cmd.AddCommand(server.NewCommand(&log))
	cmd.AddCommand(announce.NewCommand(&log))

	return cmd
}

// InitLogger returns a ready-to-use logger from the given viper configuration.
func initLogger(level, format string) (*slog.Logger, error) {
	logLevel := new(slog.LevelVar)
	logLevel.Set(slog.LevelInfo)

	hdlrType := logHandlerType(format)
	if hdlrType == "" {
		hdlrType = textHandler
	}
	logger, err := newLogger(logLevel, hdlrType)
	if err != nil {
		return nil, fmt.Errorf("failed creating logger: %w", err)
	}

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("failed parsing log level: %w", err)
	}
	logLevel.Set(lvl)

	return logger, nil
}

type logHandlerType string

const (
	jsonHandler logHandlerType = "json"
	textHandler logHandlerType = "text"
)

func newLogger(level *slog.LevelVar, hdlrType logHandlerType) (*slog.Logger, error) {
	var hdlr slog.Handler
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: false,
	}
	switch hdlrType {
	case jsonHandler:
		hdlr = slog.NewJSONHandler(os.Stdout, opts)
	case textHandler:
		hdlr = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("logging handler %q unknown", hdlrType)
	}

	return slog.New(hdlr), nil
}
