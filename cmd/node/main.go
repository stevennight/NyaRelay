package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"nyarelay/internal/node"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := node.Run(ctx, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
