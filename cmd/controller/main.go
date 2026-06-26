package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"nyarelay/internal/controller"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := controller.Run(ctx, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
