package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPodPlacementConfig_RoundTrip_Full(t *testing.T) {
	in := &PodPlacementConfig{
		NodeSelector: map[string]string{"name": "nodepool-01"},
		Tolerations: []TolerationConfig{
			{
				Key:               "dedicated",
				Operator:          "Equal",
				Value:             "gateway",
				Effect:            "NoSchedule",
				TolerationSeconds: nil,
			},
			{
				Key:               "spot",
				Operator:          "Exists",
				Effect:            "NoExecute",
				TolerationSeconds: ptrInt64(300),
			},
		},
		TopologySpreadConstraints: []TopologySpreadConstraintConfig{
			{
				MaxSkew:           1,
				TopologyKey:       "topology.kubernetes.io/zone",
				WhenUnsatisfiable: "ScheduleAnyway",
			},
		},
		PriorityClassName: "system-cluster-critical",
	}
	value, err := in.Value()
	require.NoError(t, err)

	var out PodPlacementConfig
	require.NoError(t, out.Scan(value))
	assert.Equal(t, *in, out)
}

func TestPodPlacementConfig_RoundTrip_Empty(t *testing.T) {
	in := &PodPlacementConfig{}
	value, err := in.Value()
	require.NoError(t, err)

	var out PodPlacementConfig
	require.NoError(t, out.Scan(value))
	assert.Equal(t, *in, out)
}

func TestPDBConfig_RoundTrip_MinAvailable_Int(t *testing.T) {
	in := &PDBConfig{Kind: "minAvailable", Amount: "2"}
	value, err := in.Value()
	require.NoError(t, err)

	var out PDBConfig
	require.NoError(t, out.Scan(value))
	assert.Equal(t, *in, out)
}

func TestPDBConfig_RoundTrip_MaxUnavailable_Percent(t *testing.T) {
	in := &PDBConfig{Kind: "maxUnavailable", Amount: "50%"}
	value, err := in.Value()
	require.NoError(t, err)

	var out PDBConfig
	require.NoError(t, out.Scan(value))
	assert.Equal(t, *in, out)
}

func TestDeploymentStrategyConfig_RoundTrip_Recreate(t *testing.T) {
	in := &DeploymentStrategyConfig{Type: "Recreate"}
	value, err := in.Value()
	require.NoError(t, err)

	var out DeploymentStrategyConfig
	require.NoError(t, out.Scan(value))
	assert.Equal(t, *in, out)
}

func TestDeploymentStrategyConfig_RoundTrip_RollingCustom(t *testing.T) {
	in := &DeploymentStrategyConfig{
		Type: "RollingUpdate",
		RollingUpdate: &RollingUpdateConfig{
			MaxSurge:       "25%",
			MaxUnavailable: "1",
		},
	}
	value, err := in.Value()
	require.NoError(t, err)

	var out DeploymentStrategyConfig
	require.NoError(t, out.Scan(value))
	assert.Equal(t, *in, out)
}

func TestPodScheduling_Scan_Nil(t *testing.T) {
	var p PodPlacementConfig
	assert.NoError(t, p.Scan(nil))
	var d PDBConfig
	assert.NoError(t, d.Scan(nil))
	var s DeploymentStrategyConfig
	assert.NoError(t, s.Scan(nil))
}

func TestPodScheduling_Value_EmptyMarshalsCleanly(t *testing.T) {
	c := &PodPlacementConfig{}
	v, err := c.Value()
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(v.([]byte), &raw))
}

func ptrInt64(v int64) *int64 { return &v }
