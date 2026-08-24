package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/makkes/l4proxy/cmd"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := cmd.NewRootCommand().ExecuteContext(ctx); err != nil {
		return 1
	}
	return 0
}
