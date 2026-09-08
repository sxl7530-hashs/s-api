package middleware

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemPerformanceCPUUsesGatewayProcessLoad(t *testing.T) {
	config := common.PerformanceMonitorConfig{Enabled: true, CPUThreshold: 90}

	assert.Nil(t, checkSystemPerformanceStatus(config, common.SystemStatus{
		CPUUsage:        99,
		ProcessCPUUsage: 25,
	}))

	err := checkSystemPerformanceStatus(config, common.SystemStatus{
		CPUUsage:        10,
		ProcessCPUUsage: 95,
	})
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "system cpu overloaded")
}
