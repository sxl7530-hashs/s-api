package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
)

func KlingRequestConvert() func(c *gin.Context) {
	return func(c *gin.Context) {
		var originalReq map[string]interface{}
		if err := common.UnmarshalBodyReusable(c, &originalReq); err != nil {
			c.Next()
			return
		}

		// Support both model_name and model fields
		model, _ := originalReq["model_name"].(string)
		if model == "" {
			model, _ = originalReq["model"].(string)
		}
		prompt, _ := originalReq["prompt"].(string)

		unifiedReq := map[string]interface{}{
			"model":    model,
			"prompt":   prompt,
			"metadata": originalReq,
		}

		jsonData, err := json.Marshal(unifiedReq)
		if err != nil {
			c.Next()
			return
		}

		// Rewrite request body and path
		c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))
		c.Request.URL.Path = "/v1/video/generations"

		// Determine action based on original request path
		if strings.Contains(c.FullPath(), "omni-video") {
			c.Set("action", constant.TaskActionOmniGenerate)
		} else if image, ok := originalReq["image"]; !ok || image == "" {
			c.Set("action", constant.TaskActionTextGenerate)
		}

		// Clear old body caches so subsequent UnmarshalBodyReusable reads our rewritten body.
		// Without this, the old KeyBodyStorage cache would be used, completely ignoring
		// the metadata wrapping above.
		c.Set(common.KeyBodyStorage, nil)
		c.Set(common.KeyRequestBody, jsonData)
		c.Next()
	}
}
