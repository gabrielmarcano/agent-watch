//go:build !unix

package relay

import (
	"errors"
	"os"
)

// flockExclusive is unsupported off Unix: the relay only ships for Linux, and
// running without the data-dir lock would let a CLI edit race the relay.
func flockExclusive(*os.File) error {
	return errors.New("data dir locking is not supported on this platform")
}

// matchDirOwner is a no-op off Unix.
func matchDirOwner(*os.File, string) error {
	return nil
}
