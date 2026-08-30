package services

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func TestValidatePodPlacement_AcceptsValid(t *testing.T) {
	cfg := &models.PodPlacementConfig{
		NodeSelector: map[string]string{"name": "nodepool-01"},
		Tolerations: []models.TolerationConfig{
			{Key: "dedicated", Operator: "Equal", Value: "gateway", Effect: "NoSchedule"},
		},
		TopologySpreadConstraints: []models.TopologySpreadConstraintConfig{
			{MaxSkew: 1, TopologyKey: "topology.kubernetes.io/zone", WhenUnsatisfiable: "ScheduleAnyway"},
		},
		PriorityClassName: "system-cluster-critical",
	}
	assert.NoError(t, ValidatePodPlacement(cfg))
}

func TestValidatePodPlacement_NodeSelectorBadLabelKey(t *testing.T) {
	cfg := &models.PodPlacementConfig{NodeSelector: map[string]string{"!bad": "v"}}
	assert.ErrorContains(t, ValidatePodPlacement(cfg), "nodeSelector")
}

func TestValidatePodPlacement_TolerationEqualWithoutValue(t *testing.T) {
	cfg := &models.PodPlacementConfig{
		Tolerations: []models.TolerationConfig{
			{Key: "k", Operator: "Equal", Effect: "NoSchedule"},
		},
	}
	assert.ErrorContains(t, ValidatePodPlacement(cfg), "value required when operator=Equal")
}

func TestValidatePodPlacement_TolerationSecondsWithoutNoExecute(t *testing.T) {
	seconds := int64(30)
	cfg := &models.PodPlacementConfig{
		Tolerations: []models.TolerationConfig{
			{Key: "k", Operator: "Exists", Effect: "NoSchedule", TolerationSeconds: &seconds},
		},
	}
	assert.ErrorContains(t, ValidatePodPlacement(cfg), "tolerationSeconds only meaningful with effect=NoExecute")
}

func TestValidatePodPlacement_TopologyMaxSkewZero(t *testing.T) {
	cfg := &models.PodPlacementConfig{
		TopologySpreadConstraints: []models.TopologySpreadConstraintConfig{
			{MaxSkew: 0, TopologyKey: "k", WhenUnsatisfiable: "ScheduleAnyway"},
		},
	}
	assert.ErrorContains(t, ValidatePodPlacement(cfg), "maxSkew must be >= 1")
}

func TestValidatePodPlacement_TopologyTopologyKeyEmpty(t *testing.T) {
	cfg := &models.PodPlacementConfig{
		TopologySpreadConstraints: []models.TopologySpreadConstraintConfig{
			{MaxSkew: 1, TopologyKey: "", WhenUnsatisfiable: "ScheduleAnyway"},
		},
	}
	assert.ErrorContains(t, ValidatePodPlacement(cfg), "topologyKey must be non-empty")
}

func TestValidatePodPlacement_TopologyWhenUnsatisfiableInvalid(t *testing.T) {
	cfg := &models.PodPlacementConfig{
		TopologySpreadConstraints: []models.TopologySpreadConstraintConfig{
			{MaxSkew: 1, TopologyKey: "k", WhenUnsatisfiable: "Bogus"},
		},
	}
	assert.ErrorContains(t, ValidatePodPlacement(cfg), "whenUnsatisfiable")
}

func TestValidatePDB_GoodInt(t *testing.T) {
	cfg := &models.PDBConfig{Kind: "minAvailable", Amount: "2"}
	assert.NoError(t, ValidatePDB(cfg))
}

func TestValidatePDB_GoodPercent(t *testing.T) {
	cfg := &models.PDBConfig{Kind: "maxUnavailable", Amount: "50%"}
	assert.NoError(t, ValidatePDB(cfg))
}

func TestValidatePDB_BadKind(t *testing.T) {
	cfg := &models.PDBConfig{Kind: "either", Amount: "1"}
	assert.ErrorContains(t, ValidatePDB(cfg), "kind")
}

func TestValidatePDB_BadValue_Zero(t *testing.T) {
	cfg := &models.PDBConfig{Kind: "minAvailable", Amount: "0"}
	assert.ErrorContains(t, ValidatePDB(cfg), "value")
}

func TestValidatePDB_BadValue_OverHundredPercent(t *testing.T) {
	cfg := &models.PDBConfig{Kind: "minAvailable", Amount: "150%"}
	assert.ErrorContains(t, ValidatePDB(cfg), "value")
}

func TestValidateStrategy_Recreate(t *testing.T) {
	cfg := &models.DeploymentStrategyConfig{Type: "Recreate"}
	assert.NoError(t, ValidateStrategy(cfg))
}

func TestValidateStrategy_RollingValid(t *testing.T) {
	cfg := &models.DeploymentStrategyConfig{
		Type:          "RollingUpdate",
		RollingUpdate: &models.RollingUpdateConfig{MaxSurge: "25%", MaxUnavailable: "1"},
	}
	assert.NoError(t, ValidateStrategy(cfg))
}

func TestValidateStrategy_RollingBothZero(t *testing.T) {
	cfg := &models.DeploymentStrategyConfig{
		Type:          "RollingUpdate",
		RollingUpdate: &models.RollingUpdateConfig{MaxSurge: "0%", MaxUnavailable: "0%"},
	}
	assert.ErrorContains(t, ValidateStrategy(cfg), "both maxSurge and maxUnavailable cannot be 0")
}

func TestValidateStrategy_RollingBadValue(t *testing.T) {
	cfg := &models.DeploymentStrategyConfig{
		Type:          "RollingUpdate",
		RollingUpdate: &models.RollingUpdateConfig{MaxSurge: "abc", MaxUnavailable: "1"},
	}
	assert.ErrorContains(t, ValidateStrategy(cfg), "maxSurge")
}

func TestValidateStrategy_BadType(t *testing.T) {
	cfg := &models.DeploymentStrategyConfig{Type: "Slow"}
	assert.ErrorContains(t, ValidateStrategy(cfg), "type")
}

func TestPodSchedulingValidate_NilInputsAllowed(t *testing.T) {
	assert.NoError(t, ValidatePodPlacement(nil))
	assert.NoError(t, ValidatePDB(nil))
	assert.NoError(t, ValidateStrategy(nil))
}
