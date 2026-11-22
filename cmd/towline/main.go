package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ca-lee-b/towline/internal"
)

func main() {
	var c towline.Config
	var verbose bool

	flag.StringVar(&c.URL, "endpoint", os.Getenv("TOWLINE_ENDPOINT"), "WebSocket endpoint")
	flag.StringVar(&c.Codex, "codex", "codex", "Codex executable")
	flag.StringVar(&c.Dir, "dir", ".", "Codex working directory")
	flag.BoolVar(&verbose, "verbose", false, "log command and event metadata")
	flag.Parse()

	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	c.Token = os.Getenv("TOWLINE_TOKEN")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := towline.Run(ctx, c); err != nil {
		slog.Error("Towline stopped", "error", err)
		os.Exit(1)
	}
}
