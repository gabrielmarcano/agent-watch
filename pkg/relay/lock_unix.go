//go:build unix

package relay

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// flockExclusive takes a non-blocking exclusive flock on f. It returns
// errLocked when another process holds the lock. The lock is released when f
// is closed or the process exits, so a crashed relay never leaves it stuck.
func flockExclusive(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLocked
	}
	return err
}

// syncDir fsyncs a directory, making a rename inside it durable: without it
// a crash right after the rename can bring back the old store.json.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open dir %s: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("fsync dir %s: %w", dir, err)
	}
	return nil
}

// matchDirOwner gives f the owner and group of dir when running as root.
// The CLI may be run with sudo while the relay runs as a service user; files it
// creates in the data dir must stay usable by that user. It is a no-op for
// non-root callers, whose files already belong to them.
func matchDirOwner(f *os.File, dir string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if err := f.Chown(int(st.Uid), int(st.Gid)); err != nil {
		return fmt.Errorf("chown %s: %w", f.Name(), err)
	}
	return nil
}
