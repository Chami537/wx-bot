package localapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/bridge"
	"github.com/lawrencewzen/wx-bot/internal/config"
)

// Server is the loopback-only HTTP API used while `wx-bot run` is active.
type Server struct {
	Listen   string
	Token    string
	Send     func(ctx context.Context, req bridge.SendRequest) error
	Status   func() []bridge.BindingStatus
	Log      *slog.Logger
	OnListen func(addr string)
	httpSrv  *http.Server
}

type sendBody struct {
	Binding string `json:"binding"`
	To      string `json:"to"`
	Text    string `json:"text"`
	Image   string `json:"image"`
	File    string `json:"file"`
	Video   string `json:"video"`
	Caption string `json:"caption"`
}

// New constructs a Server from config.
func New(cfg *config.Config, send func(context.Context, bridge.SendRequest) error, status func() []bridge.BindingStatus) *Server {
	return &Server{
		Listen: cfg.LocalAPI.Listen,
		Token:  cfg.LocalAPI.Token,
		Send:   send,
		Status: status,
		Log:    slog.Default().With("component", "localapi"),
	}
}

// Start binds listen (must be loopback) and serves until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if err := config.CheckLoopbackListen(s.Listen); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return fmt.Errorf("local api listen: %w", err)
	}
	// Double-check the bound address is loopback.
	if ta, ok := ln.Addr().(*net.TCPAddr); ok && ta.IP != nil && !ta.IP.IsLoopback() {
		ln.Close()
		return fmt.Errorf("local api bound to non-loopback address %s", ln.Addr())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/v1/send", s.handleSend)
	mux.HandleFunc("/v1/status", s.handleStatus)

	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log := s.Log
	if log == nil {
		log = slog.Default()
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("local api listening", "addr", ln.Addr().String())
		errCh <- s.httpSrv.Serve(ln)
	}()
	if s.OnListen != nil {
		s.OnListen(ln.Addr().String())
	}

	select {
	case <-ctx.Done():
		shctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shctx)
		<-errCh
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(w, r) {
		return
	}
	var body sendBody
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	req := bridge.SendRequest{
		Binding: body.Binding,
		To:      body.To,
		Text:    body.Text,
		Image:   body.Image,
		File:    body.File,
		Video:   body.Video,
		Caption: body.Caption,
	}
	if err := req.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.Send == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "send not configured"})
		return
	}
	if err := s.Send(r.Context(), req); err != nil {
		code := http.StatusBadGateway
		msg := err.Error()
		switch {
		case strings.Contains(msg, "unknown binding"):
			code = http.StatusNotFound
		case strings.Contains(msg, "allowlist"):
			code = http.StatusForbidden
		case strings.Contains(msg, "context_token"), strings.Contains(msg, "user must message"):
			code = http.StatusConflict
		case strings.Contains(msg, "expired"):
			code = http.StatusUnauthorized
		}
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(w, r) {
		return
	}
	var list []bridge.BindingStatus
	if s.Status != nil {
		list = s.Status()
	}
	if list == nil {
		list = []bridge.BindingStatus{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": list})
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	got := r.Header.Get("Authorization")
	want := "Bearer " + s.Token
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
