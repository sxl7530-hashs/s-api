package kling

// ================================================================
// CUSTOM: Map-based request body & omni-video routing
//
// buildRequestMap works directly with the raw metadata map instead of
// struct-based unmarshal. This avoids type mismatches (e.g. duration
// sent as number but expected as string) that silently drop fields
// like image_tail and sound.
//
// getKlingVideoPath adds omni-video routing for kling-v3-omni model.
//
// Called from:
//   - BuildRequestBody in adaptor.go
//   - BuildRequestURL / FetchTask in adaptor.go
// ================================================================

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// getKlingVideoPath returns the upstream API path for the given action.
func getKlingVideoPath(action string) string {
	switch action {
	case constant.TaskActionGenerate:
		return "/v1/videos/image2video"
	case constant.TaskActionOmniGenerate:
		return "/v1/videos/omni-video"
	default:
		return "/v1/videos/text2video"
	}
}

// buildRequestMap builds the upstream request body directly from the metadata map.
// This avoids struct-based unmarshal which breaks on type mismatches (e.g. duration
// sent as number 5 but requestPayload.Duration is string), causing ALL metadata
// fields including image_tail to be silently lost.
// By working with the raw map, any new Kling API parameter is automatically forwarded.
func (a *TaskAdaptor) buildRequestMap(req *relaycommon.TaskSubmitReq) map[string]interface{} {
	body := make(map[string]interface{})
	for k, v := range req.Metadata {
		body[k] = v
	}

	setDefault(body, "prompt", req.Prompt)
	setDefault(body, "image", req.Image)
	setDefault(body, "mode", defaultString(req.Mode, "std"))
	setDefault(body, "model_name", req.Model)
	setDefault(body, "model", req.Model)
	setDefault(body, "cfg_scale", 0.5)

	// Duration: ensure it's a string for upstream compatibility
	if dur, ok := body["duration"]; ok {
		switch v := dur.(type) {
		case float64:
			body["duration"] = fmt.Sprintf("%d", int(v))
		case int:
			body["duration"] = fmt.Sprintf("%d", v)
		}
	} else {
		body["duration"] = fmt.Sprintf("%d", defaultInt(req.Duration, 5))
	}

	if _, ok := body["aspect_ratio"]; !ok {
		body["aspect_ratio"] = a.getAspectRatio(req.Size)
	}

	if mn, _ := body["model_name"].(string); mn == "" {
		body["model_name"] = "kling-v1"
	}

	return body
}

// setDefault sets key in m only if not already present or empty
func setDefault(m map[string]interface{}, key string, value interface{}) {
	if existing, ok := m[key]; !ok || existing == nil || existing == "" {
		if value != nil && value != "" {
			m[key] = value
		}
	}
}

func defaultString(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func defaultInt(v int, def int) int {
	if v == 0 {
		return def
	}
	return v
}
