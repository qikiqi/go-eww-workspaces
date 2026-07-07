package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/qikiqi/go-eww-workspaces/internal/program"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	program.Run(ctx)
}
