//go:build darwin

package bridge

import (
	"context"
	"os/exec"
)

// ioregPath is absolute: launchd starts the bridge without a PATH.
const ioregPath = "/usr/sbin/ioreg"

// ReadPresence reads the Mac's input idle time and screen lock with ioreg.
func ReadPresence(ctx context.Context) (Presence, bool) {
	hid, err := exec.CommandContext(ctx, ioregPath, "-c", "IOHIDSystem", "-d", "4", "-r", "-k", "HIDIdleTime").Output()
	if err != nil {
		return Presence{}, false
	}
	root, err := exec.CommandContext(ctx, ioregPath, "-n", "Root", "-d1").Output()
	if err != nil {
		return Presence{}, false
	}
	return parsePresence(hid, root)
}
