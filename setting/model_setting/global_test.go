package model_setting

import (
	"bytes"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterAwsBetasConcurrentConfigurationUpdate(t *testing.T) {
	settings := GetGlobalSettings()
	common.OptionMapRWMutex.Lock()
	previousEnabled, previousBlacklist := settings.AwsBetaFilterEnabled, settings.AwsBetaBlacklist
	settings.AwsBetaFilterEnabled = true
	settings.AwsBetaBlacklist = []string{"blocked"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		settings.AwsBetaFilterEnabled, settings.AwsBetaBlacklist = previousEnabled, previousBlacklist
	})

	start := make(chan struct{})
	filtered := make(chan []string, 1)
	updated := make(chan error, 1)
	go func() {
		<-start
		filtered <- settings.FilterAwsBetas([]string{"blocked", "keep"})
	}()
	go func() {
		<-start
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		updated <- config.UpdateConfigFromMap(settings, map[string]string{
			"aws_beta_filter_enabled": "false",
			"aws_beta_blacklist":      `["keep"]`,
		})
	}()
	close(start)
	result := <-filtered
	require.NoError(t, <-updated)
	assert.Contains(t, [][]string{{"keep"}, {"blocked", "keep"}}, result,
		"a request must use one consistent filter configuration")
}

func TestShouldPreserveThinkingSuffixExactAndRegex(t *testing.T) {
	settings := GetGlobalSettings()
	original := append([]string(nil), settings.ThinkingModelBlacklist...)
	t.Cleanup(func() { settings.ThinkingModelBlacklist = original })

	assert.True(t, ShouldPreserveThinkingSuffix("kimi-k2-thinking"))
	assert.True(t, ShouldPreserveThinkingSuffix("moonshotai/kimi-k2-thinking"))
	assert.False(t, ShouldPreserveThinkingSuffix("m@sha256:abc"))

	settings.ThinkingModelBlacklist = []string{
		"kimi-k2-thinking",
		"re:[",
		"re:",
		"re:.*@sha256:.*",
	}

	var logged bytes.Buffer
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logged
	t.Cleanup(func() { gin.DefaultErrorWriter = previous })

	assert.True(t, ShouldPreserveThinkingSuffix("kimi-k2-thinking"))
	assert.True(t, ShouldPreserveThinkingSuffix("m@sha256:abc"))
	assert.False(t, ShouldPreserveThinkingSuffix("m@sha256"))
	assert.False(t, ShouldPreserveThinkingSuffix("qwen3-max@thinking:on"))
	require.Contains(t, logged.String(), `invalid thinking_model_blacklist regex "re:["`)
	require.Contains(t, logged.String(), `invalid thinking_model_blacklist regex "re:"`)

	settings.ThinkingModelBlacklist = []string{"re:^beta@"}
	assert.False(t, ShouldPreserveThinkingSuffix("m@sha256:abc"))
	assert.True(t, ShouldPreserveThinkingSuffix("beta@sha256:abc"))
	assert.False(t, ShouldPreserveThinkingSuffix("alpha@sha256:abc"))
}
