package agents

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// TailFile reads up to maxBytes from the end of the file at path.
// If the file is larger than maxBytes, the first partial line is dropped.
func TailFile(path string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 256 * 1024 // 256 KB default
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", err
	}

	size := fi.Size()
	offset := int64(0)
	dropFirstLine := false

	if size > maxBytes {
		offset = size - maxBytes
		dropFirstLine = true
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek: %w", err)
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	if dropFirstLine {
		idx := bytes.IndexByte(data, '\n')
		if idx >= 0 && idx+1 < len(data) {
			data = data[idx+1:]
		}
	}

	return string(data), nil
}
