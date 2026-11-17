package bedrockmodel

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/setting/reasoning"
)

var IDs = map[string]string{
	"claude-3-sonnet-20240229":   "anthropic.claude-3-sonnet-20240229-v1:0",
	"claude-3-opus-20240229":     "anthropic.claude-3-opus-20240229-v1:0",
	"claude-3-haiku-20240307":    "anthropic.claude-3-haiku-20240307-v1:0",
	"claude-3-5-sonnet-20240620": "anthropic.claude-3-5-sonnet-20240620-v1:0",
	"claude-3-5-sonnet-20241022": "anthropic.claude-3-5-sonnet-20241022-v2:0",
	"claude-3-5-haiku-20241022":  "anthropic.claude-3-5-haiku-20241022-v1:0",
	"claude-3-7-sonnet-20250219": "anthropic.claude-3-7-sonnet-20250219-v1:0",
	"claude-sonnet-4-20250514":   "anthropic.claude-sonnet-4-20250514-v1:0",
	"claude-opus-4-20250514":     "anthropic.claude-opus-4-20250514-v1:0",
	"claude-opus-4-1-20250805":   "anthropic.claude-opus-4-1-20250805-v1:0",
	"claude-sonnet-4-5-20250929": "anthropic.claude-sonnet-4-5-20250929-v1:0",
	"claude-sonnet-4-6":          "anthropic.claude-sonnet-4-6",
	"claude-haiku-4-5-20251001":  "anthropic.claude-haiku-4-5-20251001-v1:0",
	"claude-opus-4-5-20251101":   "anthropic.claude-opus-4-5-20251101-v1:0",
	"claude-opus-4-6":            "anthropic.claude-opus-4-6-v1",
	"claude-opus-4-7":            "anthropic.claude-opus-4-7",
	"claude-opus-4-8":            "anthropic.claude-opus-4-8",
	// Nova models
	"nova-micro-v1:0":   "amazon.nova-micro-v1:0",
	"nova-lite-v1:0":    "amazon.nova-lite-v1:0",
	"nova-pro-v1:0":     "amazon.nova-pro-v1:0",
	"nova-premier-v1:0": "amazon.nova-premier-v1:0",
	"nova-canvas-v1:0":  "amazon.nova-canvas-v1:0",
	"nova-reel-v1:0":    "amazon.nova-reel-v1:0",
	"nova-reel-v1:1":    "amazon.nova-reel-v1:1",
	"nova-sonic-v1:0":   "amazon.nova-sonic-v1:0",
}

var CrossRegion = map[string]map[string]bool{
	"anthropic.claude-3-sonnet-20240229-v1:0": {
		"us": true,
		"eu": true,
		"ap": true,
	},
	"anthropic.claude-3-opus-20240229-v1:0": {
		"us": true,
	},
	"anthropic.claude-3-haiku-20240307-v1:0": {
		"us": true,
		"eu": true,
		"ap": true,
	},
	"anthropic.claude-3-5-sonnet-20240620-v1:0": {
		"us": true,
		"eu": true,
		"ap": true,
	},
	"anthropic.claude-3-5-sonnet-20241022-v2:0": {
		"us": true,
		"ap": true,
	},
	"anthropic.claude-3-5-haiku-20241022-v1:0": {
		"us": true,
	},
	"anthropic.claude-3-7-sonnet-20250219-v1:0": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-sonnet-4-20250514-v1:0": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-opus-4-20250514-v1:0": {
		"us": true,
	},
	"anthropic.claude-opus-4-1-20250805-v1:0": {
		"us": true,
	},
	"anthropic.claude-sonnet-4-5-20250929-v1:0": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-sonnet-4-6": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-opus-4-5-20251101-v1:0": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-opus-4-6-v1": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-opus-4-7": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-opus-4-8": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	"anthropic.claude-haiku-4-5-20251001-v1:0": {
		"us": true,
		"ap": true,
		"eu": true,
	},
	// Nova models - all support three major regions
	"amazon.nova-micro-v1:0": {
		"us":   true,
		"eu":   true,
		"apac": true,
	},
	"amazon.nova-lite-v1:0": {
		"us":   true,
		"eu":   true,
		"apac": true,
	},
	"amazon.nova-pro-v1:0": {
		"us":   true,
		"eu":   true,
		"apac": true,
	},
	"amazon.nova-premier-v1:0": {
		"us": true,
	},
	"amazon.nova-canvas-v1:0": {
		"us":   true,
		"eu":   true,
		"apac": true,
	},
	"amazon.nova-reel-v1:0": {
		"us":   true,
		"eu":   true,
		"apac": true,
	},
	"amazon.nova-reel-v1:1": {
		"us": true,
	},
	"amazon.nova-sonic-v1:0": {
		"us":   true,
		"eu":   true,
		"apac": true,
	},
}

var RegionPrefixes = map[string]string{
	"us": "us",
	"eu": "eu",
	"ap": "apac",
}

// ResolveMapping follows the same chain and self-mapping rules as the relay.
func ResolveMapping(name string, mapping map[string]string) (string, error) {
	seen := map[string]bool{name: true}
	for {
		next := mapping[name]
		if next == "" {
			next = mapping[reasoning.BaseModelName(name)]
		}
		if next == "" || next == name {
			return name, nil
		}
		if seen[next] {
			return "", fmt.Errorf("model mapping contains a cycle")
		}
		seen[next] = true
		name = next
	}
}

func ID(name string) string {
	if id, ok := IDs[name]; ok {
		return id
	}
	return name
}

// FinalID exactly mirrors the AWS adaptor's existing regional profile choice.
func FinalID(name, region string) string {
	name = ID(name)
	prefix, _, _ := strings.Cut(region, "-")
	if CrossRegion[name][prefix] {
		if profile := RegionPrefixes[prefix]; profile != "" {
			return profile + "." + name
		}
	}
	return name
}
