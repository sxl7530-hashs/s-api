package service

import (
	"strings"
	"testing"
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
