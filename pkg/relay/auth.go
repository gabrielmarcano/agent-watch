package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

type contextKey string

const (
	deviceCtxKey    contextKey = "device"
	hostCtxKey      contextKey = "host"
	accessLogCtxKey contextKey = "access_log"

	pairCodeTTL       = 5 * time.Minute
	maxActiveCodes    = 3
	rateLimitWindow   = 10 * time.Minute
	maxAttemptsPerIP  = 5
	maxAttemptsGlobal = 20
)

// Sha256Hex returns the hex-encoded SHA-256 digest of input.
func Sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// GenerateDeviceToken generates 32 random bytes formatted as 64 hex characters.
// Host tokens (`hosts add`) are made the same way.
func GenerateDeviceToken() (string, error) {
	var tokenBytes [32]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(tokenBytes[:]), nil
}

type pairCodeEntry struct {
	code      string
	expiresAt time.Time
}

// HostIdentity is the host a host token belongs to.
type HostIdentity struct {
	ID   string
	Name string
	// Legacy is set for the host of AW_HOST_TOKEN, which is not in store.json.
	Legacy bool
}

// AuthManager manages tokens, pairing codes, and rate limiting.
type AuthManager struct {
	hostToken string       // AW_HOST_TOKEN; "" when unset
	legacy    HostIdentity // the host hostToken authenticates
	store     *Store
	ipPolicy  ClientIPPolicy
	now       func() time.Time // rate-limiter clock (tests override)

	mu             sync.Mutex
	pairCodes      []pairCodeEntry
	ipAttempts     map[string][]time.Time
	globalAttempts []time.Time

	sessMu   sync.Mutex
	sessions map[string]map[*deviceSession]struct{} // token hash -> in-flight device requests
}

// deviceSession is one in-flight device request (an SSE stream or an API call).
type deviceSession struct {
	cancel context.CancelFunc
}

// NewAuthManager initializes an AuthManager. hostToken (AW_HOST_TOKEN, may be
// empty) authenticates the host DefaultHostID (SetLegacyHost names another);
// the hosts in store authenticate with their own tokens. ipPolicy decides
// which address the pairing rate limiter counts attempts against.
func NewAuthManager(hostToken string, store *Store, ipPolicy ClientIPPolicy) *AuthManager {
	return &AuthManager{
		hostToken:  hostToken,
		legacy:     HostIdentity{ID: DefaultHostID, Name: DefaultHostID, Legacy: true},
		store:      store,
		ipPolicy:   ipPolicy,
		now:        time.Now,
		ipAttempts: make(map[string][]time.Time),
		sessions:   make(map[string]map[*deviceSession]struct{}),
	}
}

// trackSession registers an in-flight request made with tokenHash so that
// CloseDeviceSessions can cancel it. The returned func unregisters it.
func (a *AuthManager) trackSession(tokenHash string, cancel context.CancelFunc) func() {
	sess := &deviceSession{cancel: cancel}
	a.sessMu.Lock()
	set := a.sessions[tokenHash]
	if set == nil {
		set = make(map[*deviceSession]struct{})
		a.sessions[tokenHash] = set
	}
	set[sess] = struct{}{}
	a.sessMu.Unlock()

	return func() {
		a.sessMu.Lock()
		defer a.sessMu.Unlock()
		if set := a.sessions[tokenHash]; set != nil {
			delete(set, sess)
			if len(set) == 0 {
				delete(a.sessions, tokenHash)
			}
		}
	}
}

// CloseDeviceSessions cancels every in-flight request, SSE streams included,
// authenticated with tokenHash, and returns how many it cancelled. Call it
// after removing the device from the store.
func (a *AuthManager) CloseDeviceSessions(tokenHash string) int {
	a.sessMu.Lock()
	set := a.sessions[tokenHash]
	delete(a.sessions, tokenHash)
	a.sessMu.Unlock()

	for sess := range set {
		sess.cancel()
	}
	return len(set)
}

// ExtractBearerToken parses the Bearer token from the Authorization header.
// Query parameters are strictly ignored.
func ExtractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// SetLegacyHost sets the id and name of the host AW_HOST_TOKEN authenticates.
// Call it before serving.
func (a *AuthManager) SetLegacyHost(id, name string) {
	if name == "" {
		name = id
	}
	a.legacy = HostIdentity{ID: id, Name: name, Legacy: true}
}

// LegacyHost returns the host of AW_HOST_TOKEN; ok is false when it is unset.
func (a *AuthManager) LegacyHost() (HostIdentity, bool) {
	return a.legacy, a.hostToken != ""
}

// VerifyHostToken returns the host a token belongs to: AW_HOST_TOKEN's host,
// or a host registered in the store. Both compare in constant time (the store
// compares token hashes).
func (a *AuthManager) VerifyHostToken(token string) (HostIdentity, bool) {
	if token == "" {
		return HostIdentity{}, false
	}
	if a.hostToken != "" && len(token) == len(a.hostToken) &&
		subtle.ConstantTimeCompare([]byte(token), []byte(a.hostToken)) == 1 {
		return a.legacy, true
	}
	if a.store == nil {
		return HostIdentity{}, false
	}
	if h, ok := a.store.FindHostByTokenHash(Sha256Hex(token)); ok {
		return HostIdentity{ID: h.ID, Name: h.Name}, true
	}
	return HostIdentity{}, false
}

// HostAuthMiddleware validates the host bearer token and attaches the
// HostIdentity to the request context.
func (a *AuthManager) HostAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, ok := a.VerifyHostToken(ExtractBearerToken(r))
		if !ok {
			writeError(w, model.ErrUnauthorized, "invalid or missing host token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), hostCtxKey, host)))
	})
}

// HostFromContext extracts the authenticated host from context.
func HostFromContext(ctx context.Context) (HostIdentity, bool) {
	h, ok := ctx.Value(hostCtxKey).(HostIdentity)
	return h, ok
}

// DeviceAuthMiddleware validates the device bearer token and attaches the Device to context.
func (a *AuthManager) DeviceAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ExtractBearerToken(r)
		if token == "" {
			writeError(w, model.ErrUnauthorized, "missing device token")
			return
		}

		tokenHash := Sha256Hex(token)

		// Register the request before the lookup. A revocation removes the
		// device from the store first and then cancels its sessions, so either
		// the lookup below fails or this request is cancelled by the revocation.
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		untrack := a.trackSession(tokenHash, cancel)
		defer untrack()

		dev, ok := a.store.FindDeviceByTokenHash(tokenHash)
		if !ok {
			writeError(w, model.ErrUnauthorized, "unknown device token")
			return
		}

		a.store.TouchDevice(dev.ID)
		noteAccessLogDevice(ctx, dev.ID)
		ctx = context.WithValue(ctx, deviceCtxKey, dev)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// DeviceFromContext extracts the authenticated Device from context.
func DeviceFromContext(ctx context.Context) (Device, bool) {
	dev, ok := ctx.Value(deviceCtxKey).(Device)
	return dev, ok
}

// GeneratePairCode creates a new 6-digit random code valid for 5 minutes.
// Enforces max 3 active codes by evicting the oldest.
func (a *AuthManager) GeneratePairCode() (string, time.Time, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("crypto rand int: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())
	expiresAt := time.Now().Add(pairCodeTTL)

	a.mu.Lock()
	defer a.mu.Unlock()

	a.pruneExpiredCodesLocked()

	// If at maxActiveCodes, evict oldest
	for len(a.pairCodes) >= maxActiveCodes {
		a.pairCodes = a.pairCodes[1:]
	}

	a.pairCodes = append(a.pairCodes, pairCodeEntry{
		code:      code,
		expiresAt: expiresAt,
	})

	return code, expiresAt, nil
}

// ConsumePairCode checks and deletes a pair code if valid and not expired.
func (a *AuthManager) ConsumePairCode(code string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	for i, entry := range a.pairCodes {
		if entry.code == code && now.Before(entry.expiresAt) {
			// Remove the used code
			a.pairCodes = append(a.pairCodes[:i], a.pairCodes[i+1:]...)
			return true
		}
	}
	return false
}

// pruneExpiredCodesLocked removes expired pairing codes.
func (a *AuthManager) pruneExpiredCodesLocked() {
	now := time.Now()
	var active []pairCodeEntry
	for _, entry := range a.pairCodes {
		if now.Before(entry.expiresAt) {
			active = append(active, entry)
		}
	}
	a.pairCodes = active
}

// ClientIP extracts the client IP for rate limiting (see ClientIPPolicy).
func (a *AuthManager) ClientIP(r *http.Request) string {
	return a.ipPolicy.ClientIP(r)
}

// CheckAndRecordAttempt checks if an attempt from clientIP is within limits,
// and records the attempt if allowed. Returns false if rate limit exceeded.
func (a *AuthManager) CheckAndRecordAttempt(clientIP string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	cutoff := now.Add(-rateLimitWindow)

	// Prune every client's attempts, not only this one's, so IPs that
	// stopped trying are forgotten. The map stays small: only allowed
	// attempts are recorded, at most maxAttemptsGlobal per window.
	for ip, attempts := range a.ipAttempts {
		var active []time.Time
		for _, t := range attempts {
			if t.After(cutoff) {
				active = append(active, t)
			}
		}
		if len(active) == 0 {
			delete(a.ipAttempts, ip)
		} else {
			a.ipAttempts[ip] = active
		}
	}
	activeIP := a.ipAttempts[clientIP]

	// Prune global attempts
	var activeGlobal []time.Time
	for _, t := range a.globalAttempts {
		if t.After(cutoff) {
			activeGlobal = append(activeGlobal, t)
		}
	}
	a.globalAttempts = activeGlobal

	if len(activeIP) >= maxAttemptsPerIP || len(activeGlobal) >= maxAttemptsGlobal {
		return false
	}

	// Record this attempt
	a.ipAttempts[clientIP] = append(a.ipAttempts[clientIP], now)
	a.globalAttempts = append(a.globalAttempts, now)
	return true
}
