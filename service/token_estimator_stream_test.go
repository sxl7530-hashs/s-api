package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimateTokenReaderPreservesChunkBoundaries(t *testing.T) {
	for _, provider := range []Provider{Gemini, Claude, OpenAI} {
		text := "helloWorld 123.45\n中文 and-more 😀"
		got, err := EstimateTokenReader(provider, strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		if got != EstimateToken(provider, text) {
			t.Fatalf("provider %s: streamed=%d whole=%d", provider, got, EstimateToken(provider, text))
		}
	}
}

func TestResponseAccumulatorUsesManagedDiskCache(t *testing.T) {
	originalConfig := common.GetDiskCacheConfig()
	t.Cleanup(func() { common.SetDiskCacheConfig(originalConfig) })
	common.SetDiskCacheConfig(common.DiskCacheConfig{
		Enabled:                  true,
		Path:                     t.TempDir(),
		MaxSizeMB:                16,
		CriticalWatermarkPercent: 100,
		MaxRequestMB:             8,
	})

	var accumulator ResponseAccumulator
	_, err := accumulator.WriteString(strings.Repeat("x", responseAccumulatorMemoryLimit+1))
	require.NoError(t, err)
	require.NotNil(t, accumulator.file)
	assert.Equal(t, common.GetDiskCacheDir(), filepath.Dir(accumulator.file.Name()))
	path := accumulator.file.Name()
	require.NoError(t, accumulator.Close())
	_, err = os.Stat(path)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestEstimateTokenReaderHandlesSplitUTF8AndLongWords(t *testing.T) {
	tests := []string{
		"逐字节读取的中文、emoji 😀 和 latin123",
		strings.Repeat("a", 256<<10),
	}
	for _, provider := range []Provider{Gemini, Claude, OpenAI} {
		for _, text := range tests {
			got, err := EstimateTokenReader(provider, iotest.OneByteReader(strings.NewReader(text)))
			require.NoError(t, err)
			assert.Equal(t, EstimateToken(provider, text), got, "provider %s", provider)
		}
	}
}

func TestTokenEstimatorSymbolLookup(t *testing.T) {
	for _, symbol := range []rune{'∑', '∫', '²', '₉', '\u2A00', '\U0001D400'} {
		assert.True(t, isMathSymbol(symbol), "%q", symbol)
	}
	for _, symbol := range []rune{'a', '0', '中', ',', '@'} {
		assert.False(t, isMathSymbol(symbol), "%q", symbol)
	}
	for _, delimiter := range []rune{'/', ':', '?', '&', '=', ';', '#', '%'} {
		assert.True(t, isURLDelim(delimiter), "%q", delimiter)
	}
	assert.False(t, isURLDelim('.'))
	assert.Equal(t, multipliersMap[OpenAI], getMultipliers(Unknown))
}

func TestCountTokenInputJoinsSlicesWithoutSeparators(t *testing.T) {
	want := CountTextToken("alpha12中文", "claude-3")
	assert.Equal(t, want, CountTokenInput([]string{"alpha", "12", "中文"}, "claude-3"))
	assert.Equal(t, want, CountTokenInput([]interface{}{"alpha", 12, "中文"}, "claude-3"))
}

func TestTokenEstimatorPreservesWordStateAcrossWrites(t *testing.T) {
	estimator := NewTokenEstimator("claude-3")
	for _, chunk := range []string{"long", "Word", "12", "3", " 中", "文"} {
		estimator.WriteString(chunk)
	}
	assert.Equal(t, EstimateTokenByModel("claude-3", "longWord123 中文"), estimator.Tokens())
}

func TestStreamingTokenCounterKeepsExactOpenAITokensWithinBound(t *testing.T) {
	InitTokenEncoders()
	text := "A short OpenAI response with punctuation: 1, 2, 3."
	counter := NewStreamingTokenCounter("gpt-5")
	for _, chunk := range []string{text[:8], text[8:24], text[24:]} {
		_, err := counter.WriteString(chunk)
		require.NoError(t, err)
	}
	tokens, err := counter.Tokens()
	require.NoError(t, err)
	assert.Equal(t, CountTextToken(text, "gpt-5"), tokens)
	assert.True(t, counter.UsesExactTokenizer())
}

func TestStreamingTokenCounterLargeOutputHasConservativeBillingFloor(t *testing.T) {
	text := strings.Repeat("a", 8<<20)
	counter := NewStreamingTokenCounter("gpt-5")
	for start := 0; start < len(text); start += 32 << 10 {
		end := start + 32<<10
		if end > len(text) {
			end = len(text)
		}
		_, err := counter.WriteString(text[start:end])
		require.NoError(t, err)
	}
	tokens, err := counter.Tokens()
	require.NoError(t, err)
	assert.False(t, counter.UsesExactTokenizer())
	assert.GreaterOrEqual(t, tokens, (len(text)+7)/8)
}

func TestConcurrentLargeFilesPreserveDataAndReleaseDisk(t *testing.T) {
	originalConfig := common.GetDiskCacheConfig()
	originalMaxFileDownloadMB := constant.MaxFileDownloadMB
	t.Cleanup(func() {
		common.SetDiskCacheConfig(originalConfig)
		constant.MaxFileDownloadMB = originalMaxFileDownloadMB
	})
	common.SetDiskCacheConfig(common.DiskCacheConfig{
		Enabled:                  true,
		Path:                     t.TempDir(),
		MaxSizeMB:                512,
		CriticalWatermarkPercent: 100,
		MaxRequestMB:             64,
	})
	constant.MaxFileDownloadMB = 16

	payload := bytes.Repeat([]byte("large-file-payload-0123456789abcdef"), (8<<20)/35)
	wantHash := sha256.Sum256(payload)
	const concurrency = 16
	errors := make(chan error, concurrency)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: int64(len(payload)),
				Header:        http.Header{"Content-Type": []string{"application/octet-stream"}},
				Body:          io.NopCloser(bytes.NewReader(payload)),
			}
			cached, handled, err := loadURLDiskFirst(response, "https://example.com/file.bin")
			if err != nil {
				errors <- err
				return
			}
			if !handled || !cached.IsDisk() {
				errors <- fmt.Errorf("large file did not use disk cache")
				return
			}
			encoded, err := cached.GetBase64Data()
			if err != nil {
				errors <- err
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				errors <- err
				return
			}
			if gotHash := sha256.Sum256(decoded); gotHash != wantHash {
				errors <- fmt.Errorf("large file content hash mismatch")
				return
			}
			if err = cached.Close(); err != nil {
				errors <- err
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	stats := common.GetDiskCacheStats()
	assert.Zero(t, stats.ReservedDiskBytes)
	files, _, err := common.GetDiskCacheInfo()
	require.NoError(t, err)
	assert.Zero(t, files)
}
