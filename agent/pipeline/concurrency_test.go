// Package pipeline_test 验证 step concurrency 配置解析。
package pipeline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/pipeline"
)

func TestParseStepConcurrencyDefaultsToSerial(t *testing.T) {
	c, err := pipeline.ParseStepConcurrency("")
	require.NoError(t, err)
	assert.Equal(t, pipeline.ConcurrencySerial, c.Mode)
	assert.Equal(t, 1, c.Limit)
}

func TestParseStepConcurrencyModes(t *testing.T) {
	c, err := pipeline.ParseStepConcurrency("parallel")
	require.NoError(t, err)
	assert.Equal(t, pipeline.ConcurrencyParallel, c.Mode)

	c, err = pipeline.ParseStepConcurrency("batch:3")
	require.NoError(t, err)
	assert.Equal(t, pipeline.ConcurrencyBatch, c.Mode)
	assert.Equal(t, 3, c.Limit)
}

func TestParseStepConcurrencyRejectsInvalidBatch(t *testing.T) {
	_, err := pipeline.ParseStepConcurrency("batch:0")
	assert.Error(t, err)
	_, err = pipeline.ParseStepConcurrency("fast")
	assert.Error(t, err)
}
