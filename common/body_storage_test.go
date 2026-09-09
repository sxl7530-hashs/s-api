package common

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewReplayableBodyReaderKeepsStorageLifecycleWithCaller(t *testing.T) {
	payload := []byte(`{"model":"test-model","input":"hello"}`)
	storage, err := CreateBodyStorage(payload)
	require.NoError(t, err)
	defer storage.Close()

	body := NewReplayableBodyReader(storage)
	assert.EqualValues(t, len(payload), body.Size())
	_, exposesCloser := any(body).(io.Closer)
	assert.False(t, exposesCloser, "the request body must not expose the storage closer")

	req, err := http.NewRequest(http.MethodPost, "https://example.com", body)
	require.NoError(t, err)
	require.NoError(t, req.Body.Close())

	replayBody, err := body.NewReader()
	require.NoError(t, err, "closing the HTTP request body must not close the storage")
	replay, err := io.ReadAll(replayBody)
	require.NoError(t, err)
	require.NoError(t, replayBody.Close())
	assert.Equal(t, payload, replay)

	require.NoError(t, storage.Close())
	_, err = body.NewReader()
	require.ErrorIs(t, err, ErrStorageClosed)
}

func TestDiskCacheCleanupPreservesActiveAndOtherLiveInstances(t *testing.T) {
	originalConfig := GetDiskCacheConfig()
	originalInstanceID := diskCacheInstanceID
	t.Cleanup(func() {
		SetDiskCacheConfig(originalConfig)
		diskCacheInstanceID = originalInstanceID
		activeDiskCacheFiles = sync.Map{}
	})

	cachePath := t.TempDir()
	SetDiskCacheConfig(DiskCacheConfig{Enabled: true, Path: cachePath})
	diskCacheInstanceID = "instance-test-current"

	activePath, activeFile, err := CreateDiskCacheFile(DiskCacheTypeBody)
	require.NoError(t, err)
	require.NoError(t, activeFile.Close())
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(activePath, old, old))

	liveDir := filepath.Join(getDiskCacheRootDir(), "instance-test-live")
	require.NoError(t, os.MkdirAll(liveDir, 0o755))
	livePath := filepath.Join(liveDir, "body-live.tmp")
	require.NoError(t, os.WriteFile(livePath, []byte("live"), 0o600))
	require.NoError(t, os.Chtimes(livePath, old, old))

	staleDir := filepath.Join(getDiskCacheRootDir(), "instance-test-stale")
	require.NoError(t, os.MkdirAll(staleDir, 0o755))
	stalePath := filepath.Join(staleDir, "body-stale.tmp")
	require.NoError(t, os.WriteFile(stalePath, []byte("stale"), 0o600))
	require.NoError(t, os.Chtimes(stalePath, old, old))
	require.NoError(t, os.Chtimes(staleDir, old, old))

	require.NoError(t, CleanupOldDiskCacheFiles(30*time.Minute))
	_, err = os.Stat(activePath)
	assert.NoError(t, err)
	_, err = os.Stat(livePath)
	assert.NoError(t, err)
	_, err = os.Stat(stalePath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
