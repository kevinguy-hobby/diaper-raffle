// Command server runs the diaper raffle: a JSON API, a static page, and a
// SQLite file to keep it all in.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kevinnguyen/diaper-raffle/internal/httpapi"
	"github.com/kevinnguyen/diaper-raffle/internal/store"
	"github.com/kevinnguyen/diaper-raffle/internal/webui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr    = flag.String("addr", envOr("ADDR", ":8080"), "address to listen on")
		dbPath  = flag.String("db", envOr("DB_PATH", "diaper-raffle.db"), "path to the SQLite file")
		dev     = flag.Bool("dev", false, "serve static assets from disk instead of the binary")
		verbose = flag.Bool("verbose", false, "log at debug level")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	log.Info("database ready", "path", *dbPath)

	assetDir := ""
	if *dev {
		if assetDir = webui.DevDir(); assetDir == "" {
			return errors.New("-dev: could not find internal/webui/assets; run from the repository root")
		}
		log.Info("serving assets from disk", "dir", assetDir)
	}

	assets, index, err := webui.Assets(assetDir)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           httpapi.New(st, log, assets, index).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Generous: the odds simulation is the slowest thing here and it still
		// finishes in milliseconds.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  90 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", *addr, "url", "http://localhost"+portOf(*addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// Let a draw already in flight finish writing before the door closes.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return <-serveErr
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// portOf turns a listen address into something clickable in a terminal.
func portOf(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i:]
		}
	}
	return ":" + addr
}
