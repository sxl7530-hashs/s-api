package gemini

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func GeminiTextGenerationHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// 读取响应体
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	logger.LogDebug(c, "Gemini native response body: %s", responseBody)

	// 解析为 Gemini 原生响应格式
	var geminiResponse dto.GeminiChatResponse
	err = common.Unmarshal(responseBody, &geminiResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if len(geminiResponse.Candidates) == 0 && geminiResponse.PromptFeedback != nil && geminiResponse.PromptFeedback.BlockReason != nil {
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, fmt.Sprintf("gemini_block_reason=%s", *geminiResponse.PromptFeedback.BlockReason))
	}

	// 计算使用量（基于 UsageMetadata）
	usage := buildUsageFromGeminiMetadata(geminiResponse.UsageMetadata, info.GetEstimatePromptTokens())

	// CUSTOM: 图片生成模型检查：确保图片生成成功，否则不扣费
	if model_setting.IsGeminiModelSupportImagine(info.UpstreamModelName) {
		for _, candidate := range geminiResponse.Candidates {
			if candidate.FinishReason != nil && *candidate.FinishReason != "STOP" {
				finishReason := *candidate.FinishReason
				errMsg := fmt.Sprintf("image generation failed: finish_reason=%s", finishReason)
				common.SetContextKey(c, constant.ContextKeyAdminRejectReason, fmt.Sprintf("gemini_native_image_gen_failed_reason=%s", finishReason))
				return &usage, types.NewOpenAIError(
					errors.New(errMsg),
					types.ErrorCodeEmptyResponse,
					http.StatusInternalServerError,
					types.ErrOptionWithSkipRetry(),
				)
			}
		}
		if !hasImageInGeminiResponse(geminiResponse.Candidates) {
			errMsg := "image generation failed: no image in response (model may have refused to generate)"
			logger.LogWarn(c, errMsg)
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "gemini_native_image_gen_no_image")
			return &usage, types.NewOpenAIError(
				errors.New(errMsg),
				types.ErrorCodeEmptyResponse,
				http.StatusInternalServerError,
				types.ErrOptionWithSkipRetry(),
			)
		}
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	return &usage, nil
}

func NativeGeminiEmbeddingHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	logger.LogDebug(c, "Gemini native embedding response body: %s", responseBody)

	usage := service.ResponseText2Usage(c, "", info.UpstreamModelName, info.GetEstimatePromptTokens())

	if info.IsGeminiBatchEmbedding {
		var geminiResponse dto.GeminiBatchEmbeddingResponse
		err = common.Unmarshal(responseBody, &geminiResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	} else {
		var geminiResponse dto.GeminiEmbeddingResponse
		err = common.Unmarshal(responseBody, &geminiResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	return usage, nil
}

func GeminiTextGenerationStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	helper.SetEventStreamHeaders(c)

	usage, _, err := geminiStreamHandler(c, info, resp, func(data string, geminiResponse *dto.GeminiChatResponse) bool {
		err := helper.StringData(c, data)
		if err != nil {
			logger.LogError(c, "failed to write stream data: "+err.Error())
			return false
		}
		info.SendResponseCount++
		return true
	})
	return usage, err
}
