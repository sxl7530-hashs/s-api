package service

import (
	"bytes"
	"io"
	"os"

	"github.com/QuantumNous/new-api/common"
)

// ResponseAccumulator keeps short responses in memory and spills larger
// responses to a temporary file. It preserves the complete text for fallback
// token accounting without retaining an unbounded in-memory builder.
type ResponseAccumulator struct {
	mem         bytes.Buffer
	file        *os.File
	fileSize    int64
	err         error
	diskTracked int64
	diskBacked  bool
}

const responseAccumulatorMemoryLimit = 256 << 10

func (a *ResponseAccumulator) WriteString(value string) (int, error) {
	if a.err != nil {
		return 0, a.err
	}
	if value == "" {
		return 0, nil
	}
	if a.file == nil && a.mem.Len()+len(value) <= responseAccumulatorMemoryLimit {
		return a.mem.WriteString(value)
	}
	if a.file == nil {
		var file *os.File
		var err error
		createdOnDisk := false
		if common.IsDiskCacheEnabled() {
			_, file, err = common.CreateDiskCacheFile(common.DiskCacheTypeResponse)
			createdOnDisk = file != nil && err == nil
		}
		if file == nil {
			file, err = os.CreateTemp("", "new-api-response-*")
		}
		if err != nil {
			a.err = err
			return 0, err
		}
		a.file = file
		a.diskBacked = createdOnDisk
		if _, err = a.file.Write(a.mem.Bytes()); err != nil {
			a.err = err
			_ = a.Close()
			return 0, err
		}
		a.fileSize = int64(a.mem.Len())
		if a.diskBacked {
			common.IncrementDiskFiles(a.fileSize)
			a.diskTracked = a.fileSize
		}
		a.mem.Reset()
	}
	n, err := a.file.WriteString(value)
	a.fileSize += int64(n)
	if a.diskBacked && n > 0 {
		common.AddDiskCacheUsage(int64(n))
		a.diskTracked += int64(n)
	}
	if err != nil {
		a.err = err
	}
	return n, err
}

func (a *ResponseAccumulator) String() (string, error) {
	if a.err != nil {
		return "", a.err
	}
	if a.file == nil {
		return a.mem.String(), nil
	}
	if _, err := a.file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(a.file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// NewReader returns an independent reader over the complete accumulated
// response. The accumulator remains usable and owns the underlying file.
func (a *ResponseAccumulator) NewReader() (io.ReadCloser, error) {
	if a.err != nil {
		return nil, a.err
	}
	if a.file == nil {
		return io.NopCloser(bytes.NewReader(a.mem.Bytes())), nil
	}
	file, err := os.Open(a.file.Name())
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (a *ResponseAccumulator) Err() error { return a.err }

func (a *ResponseAccumulator) Close() error {
	if a.file == nil {
		a.mem.Reset()
		return nil
	}
	name := a.file.Name()
	err := a.err
	if closeErr := a.file.Close(); err == nil {
		err = closeErr
	}
	if a.diskTracked > 0 {
		common.DecrementDiskFiles(a.diskTracked)
		a.diskTracked = 0
	}
	if removeErr := os.Remove(name); err == nil {
		err = removeErr
	}
	a.file = nil
	a.fileSize = 0
	a.diskBacked = false
	a.mem.Reset()
	return err
}
