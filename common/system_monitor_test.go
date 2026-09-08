package common

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseCgroupCPUCapacity(t *testing.T) {
	tests := []struct {
		value string
		want  float64
		ok    bool
	}{
		{value: "200000 100000", want: 2, ok: true},
		{value: "50000 100000\n", want: 0.5, ok: true},
		{value: "max 100000", ok: false},
		{value: "-1 100000", ok: false},
		{value: "invalid", ok: false},
	}
	for _, test := range tests {
		got, ok := parseCgroupCPUCapacity(test.value)
		assert.Equal(t, test.ok, ok, test.value)
		assert.Equal(t, test.want, got, test.value)
	}
}

func TestCalculateCgroupCPUPercent(t *testing.T) {
	start := time.Unix(100, 0)
	tests := []struct {
		name     string
		previous cgroupCPUSample
		current  cgroupCPUSample
		capacity float64
		want     float64
		ok       bool
	}{
		{
			name:     "half of two cpu quota",
			previous: cgroupCPUSample{usageSeconds: 10, at: start},
			current:  cgroupCPUSample{usageSeconds: 15, at: start.Add(5 * time.Second)},
			capacity: 2,
			want:     50,
			ok:       true,
		},
		{
			name:     "quota saturation is capped",
			previous: cgroupCPUSample{usageSeconds: 10, at: start},
			current:  cgroupCPUSample{usageSeconds: 12, at: start.Add(time.Second)},
			capacity: 0.5,
			want:     100,
			ok:       true,
		},
		{
			name:     "counter reset",
			previous: cgroupCPUSample{usageSeconds: 10, at: start},
			current:  cgroupCPUSample{usageSeconds: 1, at: start.Add(time.Second)},
			capacity: 1,
			ok:       false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := calculateCgroupCPUPercent(test.previous, test.current, test.capacity)
			assert.Equal(t, test.ok, ok)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestCalculateCgroupMemoryUsage(t *testing.T) {
	percent, ok := calculateCgroupMemoryUsage("500\n", "1000\n", 2000)
	assert.True(t, ok)
	assert.Equal(t, 50.0, percent)

	percent, ok = calculateCgroupMemoryUsage("1200", "1000", 2000)
	assert.True(t, ok)
	assert.Equal(t, 100.0, percent)

	_, ok = calculateCgroupMemoryUsage("500", "max", 2000)
	assert.False(t, ok)
	_, ok = calculateCgroupMemoryUsage("500", "3000", 2000)
	assert.False(t, ok)
}

func TestNormalizeProcessCPUUsage(t *testing.T) {
	assert.Equal(t, 60.0, normalizeProcessCPUUsage(120, 2))
	assert.Equal(t, 100.0, normalizeProcessCPUUsage(50, 0.5))
	assert.Zero(t, normalizeProcessCPUUsage(math.NaN(), 2))
	assert.Zero(t, normalizeProcessCPUUsage(50, 0))
}
