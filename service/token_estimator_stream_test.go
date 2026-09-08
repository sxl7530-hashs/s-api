package service

import (
	"strings"
	"testing"
	"testing/iotest"

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
