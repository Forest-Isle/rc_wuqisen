package main

import (
	"context"
	"fmt"
	"github.com/wuqisen/reliable-notification-service/internal/app"
	"github.com/wuqisen/reliable-notification-service/internal/config"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	c, e := config.Load()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if e = app.Run(ctx, c); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
