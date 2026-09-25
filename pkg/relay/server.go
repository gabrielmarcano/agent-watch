package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/push"
)

const (
	// shutdownTimeout bounds a graceful stop (systemd's stop timeout is longer).
	shutdownTimeout = 10 * time.Second
	// readHeaderTimeoutDefault bounds reading a request's headers.
	readHeaderTimeoutDefault = 10 * time.Second
	// idleTimeoutDefault closes keep-alive connections idle between requests.
	idleTimeoutDefault = 120 * time.Second
)

// newFCMSender builds the FCM sender from the service-account JSON. The relay
// hands it the store's token list and its dead-token callback. Tests replace
// it to check that wiring without talking to Google.
var newFCMSender = func(ctx context.Context, credsJSON []byte, tokens func() []string, onInvalidToken func(string)) (push.Sender, string, error) {
	f, err := push.NewFCMFromCredentials(ctx, credsJSON, tokens, onInvalidToken)
	if err != nil {
		return nil, "", err
	}
	return f, f.ProjectID, nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Server coordinates the relay components and serves the HTTP API.
type Server struct {
	cfg               *Config
	store             *Store
	state             *State
	auth              *AuthManager
	hub               *Hub
	handler           http.Handler
	keepAliveInterval time.Duration
	sseWriteTimeout   time.Duration // per SSE event; 0 means sseWriteTimeoutDefault
	readHeaderTimeout time.Duration // 0 means readHeaderTimeoutDefault
	idleTimeout       time.Duration // 0 means idleTimeoutDefault

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
	auth := NewAuthManager(cfg.HostToken, store, cfg.ClientIPPolicy())

	var senders []push.Sender
	if cfg.FCMCredentials != "" {
		credsData, err := os.ReadFile(cfg.FCMCredentials)
		if err != nil {
			return nil, fmt.Errorf("read fcm credentials from %s: %w", cfg.FCMCredentials, err)
		}
		fcmSender, projectID, err := newFCMSender(context.Background(), credsData, store.AllFCMTokens, store.RemoveFCMToken)
		if err != nil {
			return nil, fmt.Errorf("init fcm sender: %w", err)
		}
		senders = append(senders, fcmSender)
		slog.Info("fcm push enabled", "project_id", projectID)
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
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		if cerr := s.Close(); cerr != nil {
			slog.Error("error closing store", "err", cerr)
		}
		return fmt.Errorf("listen on %s: %w", s.cfg.ListenAddr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve is Run on an existing listener, which it takes over and closes.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	defer func() {
		if err := s.Close(); err != nil {
			slog.Error("error closing store during shutdown", "err", err)
		}
	}()

	adminLn, err := listenAdmin(s.cfg.DataDir)
	if err != nil {
		_ = ln.Close()
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

	// Every request context derives from reqCtx, which shutdown cancels at
	// once (RegisterOnShutdown). Without it Shutdown waits out its budget on
	// open SSE streams, and it never touches the hijacked host WebSocket.
	reqCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()

	srv := &http.Server{
		Handler: s.handler,
		// Both only apply outside a handler (headers of a new request, a
		// keep-alive connection between requests), so they never cut an SSE
		// stream or the hijacked host WebSocket. No ReadTimeout and no
		// WriteTimeout: those would. SSE writes have their own deadline.
		ReadHeaderTimeout: orDefault(s.readHeaderTimeout, readHeaderTimeoutDefault),
		IdleTimeout:       orDefault(s.idleTimeout, idleTimeoutDefault),
		BaseContext:       func(net.Listener) context.Context { return reqCtx },
	}
	srv.RegisterOnShutdown(cancelRequests)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("relay server listening", "addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if runErr == nil {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("graceful shutdown did not finish, closing connections", "err", err)
			_ = srv.Close()
			runErr = err
		}
	}
	// The host handler must be done before the deferred Close saves the store.
	if err := s.hub.Shutdown(shutdownCtx); err != nil {
		slog.Error("host connection did not close in time", "err", err)
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
