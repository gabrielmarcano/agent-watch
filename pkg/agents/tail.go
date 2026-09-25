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
	return tailRead(f, fi.Size(), maxBytes)
}

// tailRead reads the last maxBytes of r as it was when its size was size.
// Bytes appended after that (the agent still writing) are not read, so the
// read is bounded even if the file grows meanwhile.
func tailRead(r io.ReadSeeker, size, maxBytes int64) (string, error) {
	offset := int64(0)
	dropFirstLine := false

	if size > maxBytes {
		offset = size - maxBytes
		dropFirstLine = true
	}

	if _, err := r.Seek(offset, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek: %w", err)
	}

	data, err := io.ReadAll(io.LimitReader(r, size-offset))
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
