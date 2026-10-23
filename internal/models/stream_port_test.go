package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamPorts_ValueNilIsEmptyArray(t *testing.T) {
	var p StreamPorts
	v, err := p.Value()
	require.NoError(t, err)
	assert.Equal(t, []byte("[]"), v)
}

func TestStreamPorts_ScanRoundTrip(t *testing.T) {
	in := StreamPorts{{Transport: "TCP", Port: 80}, {Transport: "UDP", Port: 53}}
	v, err := in.Value()
	require.NoError(t, err)

	var out StreamPorts
	require.NoError(t, out.Scan(v))
	assert.Equal(t, in, out)

	var fromString StreamPorts
	require.NoError(t, fromString.Scan(`[{"transport":"TCP","port":81}]`))
	assert.Equal(t, StreamPorts{{Transport: "TCP", Port: 81}}, fromString)

	var fromNil StreamPorts
	require.NoError(t, fromNil.Scan(nil))
	assert.Nil(t, fromNil)

	assert.Error(t, (&StreamPorts{}).Scan(42))
}

func TestStreamPorts_Has(t *testing.T) {
	p := StreamPorts{{Transport: "TCP", Port: 53}}
	assert.True(t, p.Has("TCP", 53))
	assert.False(t, p.Has("UDP", 53))
	assert.False(t, p.Has("TCP", 54))
}

func TestStreamPorts_Normalize(t *testing.T) {
	in := StreamPorts{{Transport: "udp", Port: 53}, {Transport: "tcp", Port: 9000}, {Transport: "TCP", Port: 80}}
	got := in.Normalize()
	assert.Equal(t, StreamPorts{{Transport: "TCP", Port: 80}, {Transport: "TCP", Port: 9000}, {Transport: "UDP", Port: 53}}, got)
	// the input is not mutated
	assert.Equal(t, "udp", in[0].Transport)
}
