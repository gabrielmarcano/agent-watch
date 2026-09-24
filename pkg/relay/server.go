package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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
}

// NewServer initializes a new Server using cfg.
func NewServer(cfg *Config) (*Server, error) {
	store, err := NewStore(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}

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
		slog.Info("ntfy push enabled", "url", cfg.NtfyURL, "topic", cfg.NtfyTopic)
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

// Run starts the HTTP server and blocks until ctx is canceled, then performs graceful shutdown.
func (s *Server) Run(ctx context.Context) error {
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

	select {
	case <-ctx.Done():
		slog.Info("shutting down relay server")
	case err := <-errCh:
		return fmt.Errorf("listen and serve: %w", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	shutdownErr := srv.Shutdown(shutdownCtx)

	// Flush and close the store
	if storeErr := s.store.Close(); storeErr != nil {
		slog.Error("error closing store during shutdown", "err", storeErr)
	}

	return shutdownErr
}
