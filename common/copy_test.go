package common

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeepCopyRawMessageUsesIndependentStorage(t *testing.T) {
	type payload struct {
		Direct json.RawMessage
		Items  []json.RawMessage
		Fields map[string]json.RawMessage
	}
	source := &payload{
		Direct: json.RawMessage(`{"direct":true}`),
		Items:  []json.RawMessage{nil, {}, json.RawMessage(`{"item":true}`)},
		Fields: map[string]json.RawMessage{"nil": nil, "empty": {}, "value": json.RawMessage(`{"field":true}`)},
	}

	clone, err := DeepCopy(source)
	require.NoError(t, err)
	require.Equal(t, source, clone)

	clone.Direct[2] = 'X'
	clone.Items[2][2] = 'X'
	clone.Fields["value"][2] = 'X'
	assert.Equal(t, json.RawMessage(`{"direct":true}`), source.Direct)
	assert.Equal(t, json.RawMessage(`{"item":true}`), source.Items[2])
	assert.Equal(t, json.RawMessage(`{"field":true}`), source.Fields["value"])
	assert.Nil(t, clone.Items[0])
	assert.NotNil(t, clone.Items[1])
	assert.Nil(t, clone.Fields["nil"])
	assert.NotNil(t, clone.Fields["empty"])
}
