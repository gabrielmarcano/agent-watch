//go:build !darwin

package bridge

import "context"

// ReadPresence reports nothing outside macOS: the relay pushes as usual.
func ReadPresence(ctx context.Context) (Presence, bool) {
	return Presence{}, false
}
