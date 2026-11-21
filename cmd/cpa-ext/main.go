package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cpa-ext/internal/admin"
	"cpa-ext/internal/audit"
	"cpa-ext/internal/billing"
	"cpa-ext/internal/cpa"
	"cpa-ext/internal/gateway"
	"cpa-ext/internal/store"
	"cpa-ext/internal/usage"
	"cpa-ext/web"
)

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func positive(name string, fallback int64) int64 {
	v, err := strconv.ParseInt(env(name, strconv.FormatInt(fallback, 10)), 10, 64)
	if err != nil || v <= 0 || v > 1_000_000 {
		log.Fatalf("invalid %s", name)
	}
	return v
}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	root, err := filepath.Abs(env("STATE_DIR", "state"))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(root, "process.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("state directory already in use")
	}
	db, err := store.Open(root)
	if err != nil {
		return err
	}
	defer db.Close()
	ledger, err := billing.New(db)
	if err != nil {
		return err
	}
	defer func() {
		if err := ledger.Close(); err != nil {
			log.Printf("release unused leases: %v", err)
		}
	}()
	upstream, err := url.Parse(strings.TrimRight(env("CPA_BASE_URL", "http://127.0.0.1:8317"), "/"))
	if err != nil || upstream.Host == "" || (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Path != "" || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" {
		return errors.New("invalid CPA_BASE_URL")
	}
	password, secret := os.Getenv("ADMIN_PASSWORD"), os.Getenv("CPA_MANAGEMENT_KEY")
	if len(password) < 20 || len(secret) < 20 || password == secret {
		return errors.New("set distinct ADMIN_PASSWORD and CPA_MANAGEMENT_KEY of at least 20 characters")
	}
	archive, err := audit.New(db, root, time.Duration(positive("AUDIT_KEEP_DAYS", 14))*24*time.Hour, positive("AUDIT_MAX_GIB", 20)<<30, positive("DISK_MIN_GIB", 10)<<30)
	if err != nil {
		return err
	}
	collector := usage.New(db, cpa.Client{Base: upstream.String(), Secret: secret, HTTP: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, ledger)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	collected := make(chan struct{})
	go func() { defer close(collected); collector.Run(ctx) }()
	front := gateway.New(upstream, ledger, archive)
	adm := admin.Service{DB: db, Ledger: ledger, Audit: archive, Collector: collector, Password: password, Assets: web.Assets()}
	serve := func(addr string, h http.Handler) *http.Server {
		return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(_ net.Listener) context.Context { return ctx }}
	}
	servers := []*http.Server{
		serve(env("GATEWAY_ADDR", "127.0.0.1:8480"), front),
		serve(env("ADMIN_ADDR", "127.0.0.1:8481"), adm.Handler("")),
	}
	if addr := os.Getenv("PANEL_ADDR"); addr != "" {
		servers = append(servers, serve(addr, admin.Panel(upstream, adm.Handler(admin.PanelPath))))
	}
	serverErrors := make(chan error, len(servers))
	for _, srv := range servers {
		go func() { log.Printf("listen %s", srv.Addr); serverErrors <- srv.ListenAndServe() }()
	}
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serverErrors:
		cancel()
	}
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdown); err != nil {
			return err
		}
	}
	if err := front.Drain(shutdown); err != nil {
		return err
	}
	archive.Close()
	<-collected
	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}
