package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ca-lee-b/towline/internal"
)

func main() {
	var c towline.Config

	flag.StringVar(&c.URL, "endpoint", os.Getenv("TOWLINE_ENDPOINT"), "WebSocket endpoint")
	flag.StringVar(&c.Codex, "codex", "codex", "Codex executable")
	flag.StringVar(&c.Dir, "dir", ".", "Codex working directory")
	flag.Parse()

	c.Token = os.Getenv("TOWLINE_TOKEN")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := towline.Run(ctx, c); err != nil {
		log.Fatal(err)
	}
}
