package kitutil

import (
	"bytes"
	"io"
	"os"
	"runtime"
)

// SpillStringWriter keeps short text in memory and transparently spills large
// streams to a temporary file. String still returns the complete value, while
// the long-lived accumulator remains bounded in heap memory.
type SpillStringWriter struct {
	mem  bytes.Buffer
	file *os.File
}

const spillStringMemoryLimit = 256 << 10

func NewSpillStringWriter() *SpillStringWriter {
	w := &SpillStringWriter{}
	runtime.SetFinalizer(w, func(v *SpillStringWriter) { _ = v.Close() })
	return w
}

func (w *SpillStringWriter) WriteString(s string) (int, error) {
	if w.file == nil && w.mem.Len()+len(s) <= spillStringMemoryLimit {
		return w.mem.WriteString(s)
	}
	if w.file == nil {
		f, err := os.CreateTemp("", "new-api-relaykit-*")
		if err != nil {
			return 0, err
		}
		w.file = f
		if _, err = f.Write(w.mem.Bytes()); err != nil {
			_ = w.Close()
			return 0, err
		}
		w.mem.Reset()
	}
	return w.file.WriteString(s)
}

func (w *SpillStringWriter) String() string {
	if w == nil {
		return ""
	}
	if w.file == nil {
		return w.mem.String()
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	b, err := io.ReadAll(w.file)
	if err != nil {
		return ""
	}
	return string(b)
}

func (w *SpillStringWriter) Len() int {
	if w == nil {
		return 0
	}
	if w.file == nil {
		return w.mem.Len()
	}
	info, err := w.file.Stat()
	if err != nil {
		return 0
	}
	return int(info.Size())
}

// NewReader returns an independent reader over the complete value without
// materializing a spilled file in memory.
func (w *SpillStringWriter) NewReader() (io.ReadCloser, error) {
	if w == nil {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	if w.file == nil {
		return io.NopCloser(bytes.NewReader(w.mem.Bytes())), nil
	}
	return os.Open(w.file.Name())
}

func (w *SpillStringWriter) Reset() {
	if w == nil {
		return
	}
	if w.file != nil {
		_ = w.file.Truncate(0)
		_, _ = w.file.Seek(0, io.SeekStart)
	}
	w.mem.Reset()
}

func (w *SpillStringWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mem.Reset()
	if w.file == nil {
		return nil
	}
	name := w.file.Name()
	err := w.file.Close()
	if removeErr := os.Remove(name); err == nil {
		err = removeErr
	}
	w.file = nil
	return err
}
