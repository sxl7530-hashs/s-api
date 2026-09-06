package gemini

// ================================================================
// CUSTOM: Image generation billing protection & reverse proxy URL support
//
// These functions detect whether a Gemini response actually contains
// generated images. Used to avoid billing when image generation fails
// (model refuses, finishReason != STOP, or no image in response).
//
// Also supports reverse-proxy channels that return image URLs in
// markdown format instead of inlineData base64.
//
// Called from:
//   - GeminiChatHandler (non-streaming) in relay-gemini.go
//   - GeminiChatStreamHandler (streaming) in relay-gemini.go
//   - GeminiTextGenerationHandler (native) in relay-gemini-native.go
// ================================================================

import (
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// hasImageURLInText checks if text contains a markdown image with either
// an HTTP(S) URL or a base64 data URI:
//
//	![alt](https://example.com/image.png)
//	![alt](data:image/jpeg;base64,/9j/4AAQ...)
func hasImageURLInText(text string) bool {
	idx := strings.Index(text, "![")
	if idx == -1 {
		return false
	}
	rest := text[idx:]
	// 兼容 http(s) URL 和 data:image base64 URI
	bracketIdx := strings.Index(rest, "](http")
	if bracketIdx == -1 {
		bracketIdx = strings.Index(rest, "](data:image/")
	}
	if bracketIdx == -1 {
		return false
	}
	closeIdx := strings.Index(rest[bracketIdx+2:], ")")
	return closeIdx != -1
}

// hasImageInGeminiResponse checks if any candidate part contains image data,
// either as inlineData or as a markdown image URL in text
func hasImageInGeminiResponse(candidates []dto.GeminiChatCandidate) bool {
	for _, candidate := range candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil && strings.HasPrefix(part.InlineData.MimeType, "image") {
				return true
			}
			if part.Text != "" && hasImageURLInText(part.Text) {
				return true
			}
		}
	}
	return false
}
