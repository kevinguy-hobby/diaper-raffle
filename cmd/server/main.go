// Command server runs the diaper raffle: a JSON API, a static page, and a
// SQLite file to keep it all in.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

		setPassword   = flag.Bool("set-password", false, "read a shared password from stdin, store its hash, and exit")
		clearPassword = flag.Bool("clear-password", false, "remove the shared password, making the site open, and exit")
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

	if *setPassword || *clearPassword {
		return managePassword(ctx, st, *setPassword)
	}

	assetDir := ""
	if *dev {
		if assetDir = webui.DevDir(); assetDir == "" {
			return errors.New("-dev: could not find internal/webui/assets; run from the repository root")
		}
		log.Info("serving assets from disk", "dir", assetDir)
	}

	assets, index, login, err := webui.Assets(assetDir)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           httpapi.New(st, log, assets, index, login).Handler(),
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

// managePassword sets or clears the shared password and exits.
//
// The password arrives on stdin rather than as a flag, deliberately: a flag
// would land in shell history and in `ps` output for every user on the
// machine. Only the derived hash is ever written down.
func managePassword(ctx context.Context, st *store.Store, setting bool) error {
	if !setting {
		if err := st.ClearPassword(ctx); err != nil {
			return err
		}
		// Existing cookies must stop working, or removing the password would
		// leave old sessions valid against a lock that no longer exists.
		if err := st.RotateSessionKey(ctx); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "password removed — the site is open to anyone with the link")
		return nil
	}

	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	password := strings.TrimRight(string(raw), "\r\n")
	if strings.TrimSpace(password) == "" {
		return errors.New("the password cannot be empty")
	}

	if err := st.SetPassword(ctx, password); err != nil {
		return err
	}
	// Anyone signed in under the old password should be signed out.
	if err := st.RotateSessionKey(ctx); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "password set (%d characters) — everyone signed in has been signed out\n",
		len([]rune(password)))
	return nil
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
