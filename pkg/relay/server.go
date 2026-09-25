package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/push"
)

// Server coordinates the relay components and serves the HTTP API.
type Server struct {
	cfg               *Config
	store             *Store
	state             *State
	auth              *AuthManager
	hub               *Hub
	handler           http.Handler
	keepAliveInterval time.Duration

	lock      *dataDirLock // nil for servers built with NewServerWithDeps
	closeOnce sync.Once
}

// NewServer initializes a new Server using cfg. It takes the exclusive lock on
// cfg.DataDir before loading the store, and keeps it until Close.
func NewServer(cfg *Config) (_ *Server, err error) {
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", cfg.DataDir, err)
	}
	lock, err := lockDataDir(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("data dir %s: %w", cfg.DataDir, err)
	}
	store, err := NewStore(cfg.DataDir)
	if err != nil {
		lock.release()
		return nil, fmt.Errorf("init store: %w", err)
	}
	defer func() {
		if err != nil {
			_ = store.Close()
			lock.release()
		}
	}()

	state := NewState()
	auth := NewAuthManager(cfg.HostToken, store, cfg.TrustCFIP)

	var senders []push.Sender
	if cfg.FCMCredentials != "" {
		credsData, err := os.ReadFile(cfg.FCMCredentials)
		if err != nil {
			return nil, fmt.Errorf("read fcm credentials from %s: %w", cfg.FCMCredentials, err)
		}
		fcmSender, err := push.NewFCMFromCredentials(context.Background(), credsData, store.AllFCMTokens, store.RemoveFCMToken)
		if err != nil {
			return nil, fmt.Errorf("init fcm sender: %w", err)
		}
		senders = append(senders, fcmSender)
		slog.Info("fcm push enabled", "project_id", fcmSender.ProjectID)
	}

	if cfg.NtfyURL != "" && cfg.NtfyTopic != "" {
		ntfySender := &push.Ntfy{
			BaseURL: cfg.NtfyURL,
			Topic:   cfg.NtfyTopic,
			Token:   cfg.NtfyToken,
		}
		senders = append(senders, ntfySender)
		// Never log the topic: it is a secret (anyone who knows it can read the pushes).
		slog.Info("ntfy push enabled", "url", cfg.NtfyURL)
	}

	var notifier Notifier
	if len(senders) > 0 {
		notifier = push.NewDispatcher(senders, nil, nil)
	} else {
		slog.Info("push disabled")
	}

	hub := NewHub(auth, state, store, notifier)

	s := &Server{
		cfg:               cfg,
		store:             store,
		state:             state,
		auth:              auth,
		hub:               hub,
		keepAliveInterval: 15 * time.Second,
		lock:              lock,
	}
	s.handler = s.routes()
	return s, nil
}

// NewServerWithDeps initializes a Server with explicit components (useful for tests).
func NewServerWithDeps(cfg *Config, store *Store, state *State, auth *AuthManager, hub *Hub) *Server {
	s := &Server{
		cfg:               cfg,
		store:             store,
		state:             state,
		auth:              auth,
		hub:               hub,
		keepAliveInterval: 15 * time.Second,
	}
	s.handler = s.routes()
	return s
}

// SetKeepAliveInterval sets the keepalive tick interval for SSE connections.
func (s *Server) SetKeepAliveInterval(d time.Duration) {
	s.keepAliveInterval = d
}

// Handler returns the HTTP handler for the server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Store returns the underlying store.
func (s *Server) Store() *Store {
	return s.store
}

// State returns the underlying state.
func (s *Server) State() *State {
	return s.state
}

// Auth returns the underlying AuthManager.
func (s *Server) Auth() *AuthManager {
	return s.auth
}

// Hub returns the underlying Hub.
func (s *Server) Hub() *Hub {
	return s.hub
}

// Run starts the HTTP server and the local admin socket, and blocks until ctx
// is canceled, then performs graceful shutdown. It always closes the Server
// (store flushed, data-dir lock released) before returning.
func (s *Server) Run(ctx context.Context) error {
	defer func() {
		if err := s.Close(); err != nil {
			slog.Error("error closing store during shutdown", "err", err)
		}
	}()

	adminLn, err := listenAdmin(s.cfg.DataDir)
	if err != nil {
		return err
	}
	adminSrv := &http.Server{
		Handler:           s.adminHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := adminSrv.Serve(adminLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("admin socket server stopped", "err", err)
		}
	}()

	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Explicitly NO WriteTimeout as SSE and WebSocket are long-lived
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("relay server listening", "addr", s.cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	var runErr error
	select {
	case <-ctx.Done():
		slog.Info("shutting down relay server")
	case err := <-errCh:
		runErr = fmt.Errorf("listen and serve: %w", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if runErr == nil {
		runErr = srv.Shutdown(shutdownCtx)
	}
	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("error shutting down admin socket", "err", err)
	}
	return runErr
}

// Close flushes and closes the store, then releases the data-dir lock. Run
// calls it on exit; call it directly only for a Server that is never Run.
// It is safe to call more than once.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.store.Close()
		if s.lock != nil {
			s.lock.release()
		}
	})
	return err
}
