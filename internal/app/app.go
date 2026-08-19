package app

import (
	"context"
	"errors"
	"github.com/Forest-Isle/rc_wuqisen/internal/api"
	"github.com/Forest-Isle/rc_wuqisen/internal/config"
	"github.com/Forest-Isle/rc_wuqisen/internal/delivery"
	"github.com/Forest-Isle/rc_wuqisen/internal/migrate"
	"github.com/Forest-Isle/rc_wuqisen/internal/observability"
	"github.com/Forest-Isle/rc_wuqisen/internal/store"
	"github.com/Forest-Isle/rc_wuqisen/internal/target"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func Run(ctx context.Context, c config.Config) error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	dbctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	st, e := store.Open(dbctx, c.DatabaseURL)
	if e != nil {
		return e
	}
	defer st.Close()
	if e = migrate.Run(dbctx, st.Pool()); e != nil {
		return e
	}
	policy := target.Policy{AllowHTTP: c.AllowHTTP, AllowPrivate: c.AllowPrivate, Allowlist: c.Allowlist}
	metrics := observability.New(st)
	client := delivery.NewClient(policy, c.RequestTimeout)
	worker := delivery.NewWorker(st, client, metrics, log, c.Workers, c.MaxAttempts, c.PollInterval, c.LeaseDuration, c.InitialBackoff, c.MaxBackoff)
	handler := api.New(st, policy, c.APIToken, c.MaxBodyBytes, metrics, log).Handler()
	srv := &http.Server{Addr: c.ListenAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	workerDone := make(chan struct{})
	go func() {
		worker.Run(workerCtx)
		close(workerDone)
	}()
	errs := make(chan error, 1)
	go func() { log.Info("server listening", "address", c.ListenAddr); errs <- srv.ListenAndServe() }()
	select {
	case e := <-errs:
		if !errors.Is(e, http.ErrServerClosed) {
			return e
		}
		return nil
	case <-ctx.Done():
	}
	stopWorker()
	sdctx, sdCancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer sdCancel()
	serverErr := srv.Shutdown(sdctx)
	select {
	case <-workerDone:
	case <-sdctx.Done():
		if serverErr == nil {
			serverErr = sdctx.Err()
		}
	}
	return serverErr
}
