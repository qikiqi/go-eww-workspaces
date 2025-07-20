package main

import (
	"context"

	"github.com/qikiqi/go-eww-workspaces/internal/program"
)

func main() {
	ctx := context.Background()
	program.Run(ctx)
}
