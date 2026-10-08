package main

import (
	"context"
	"innodb-go-reader/internal/cli"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// Return the documented runtime exit code on a broken output pipe.
	signal.Ignore(syscall.SIGPIPE)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
