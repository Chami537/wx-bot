//go:build linux || darwin

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/lawrencewzen/wx-bot/internal/bridge"
	"github.com/lawrencewzen/wx-bot/internal/config"
	"github.com/lawrencewzen/wx-bot/internal/localapi"
	"github.com/lawrencewzen/wx-bot/internal/state"
)

func cmdRun(configPath string, args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(os.Stderr, "usage: wx-bot run\n")
		return 2
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot: %v\n", err)
		return 1
	}
	if err := state.MkdirAll0700(cfg.MediaDir); err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot: media dir: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := slog.Default()
	rt := newRunTime(cfg)

	var wg sync.WaitGroup
	for _, b := range cfg.Bindings {
		b := b
		store, err := state.Open(cfg.StateDir, b.Name)
		if err != nil {
			log.Error("open binding state", "binding", b.Name, "err", err)
			continue
		}
		if store.IsExpired() {
			log.Error("binding " + b.Name + " session expired: run `wx-bot login " + b.Name + "`")
			continue
		}
		if !store.HasSession() {
			log.Info("binding not logged in, skip", "binding", b.Name)
			continue
		}
		lb, err := bridge.OpenLive(cfg, b.Name)
		if err != nil {
			log.Error("open live binding", "binding", b.Name, "err", err)
			continue
		}
		rt.put(lb)

		wh := &bridge.Dispatcher{
			Webhook: b.Webhook,
			Log:     log.With("component", "webhook", "binding", b.Name),
		}
		poller := &bridge.Poller{
			Name:             b.Name,
			Client:           lb.Client,
			Store:            lb.Store,
			Allow:            lb.Allow,
			ForwardUnallowed: b.ForwardUnallowed,
			Webhook:          wh,
			MediaDir:         cfg.MediaDir,
			Log:              log.With("component", "poller", "binding", b.Name),
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			poller.Run(ctx)
		}()
	}

	api := localapi.New(cfg, rt.send, func() []bridge.BindingStatus {
		return bridge.StatusAll(cfg)
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := api.Start(ctx); err != nil {
			log.Error("local api", "err", err)
		}
	}()

	log.Info("wx-bot running", "bindings", rt.len())
	<-ctx.Done()
	log.Info("shutting down")
	wg.Wait()
	return 0
}

type runTime struct {
	cfg  *config.Config
	mu   sync.RWMutex
	live map[string]*bridge.LiveBinding
}

func newRunTime(cfg *config.Config) *runTime {
	return &runTime{cfg: cfg, live: map[string]*bridge.LiveBinding{}}
}

func (r *runTime) put(lb *bridge.LiveBinding) {
	r.mu.Lock()
	r.live[lb.Name] = lb
	r.mu.Unlock()
}

func (r *runTime) len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.live)
}

func (r *runTime) send(ctx context.Context, req bridge.SendRequest) error {
	r.mu.RLock()
	lb, ok := r.live[req.Binding]
	r.mu.RUnlock()
	if !ok {
		var err error
		lb, err = bridge.OpenLive(r.cfg, req.Binding)
		if err != nil {
			return err
		}
	}
	return lb.Sender().Send(ctx, req)
}
