package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

type PodPlacementConfig struct {
	NodeSelector              map[string]string                `json:"nodeSelector,omitempty"`
	Tolerations               []TolerationConfig               `json:"tolerations,omitempty"`
	TopologySpreadConstraints []TopologySpreadConstraintConfig `json:"topologySpreadConstraints,omitempty"`
	PriorityClassName         string                           `json:"priorityClassName,omitempty"`
}

type TolerationConfig struct {
	Key               string `json:"key,omitempty"`
	Operator          string `json:"operator,omitempty"`
	Value             string `json:"value,omitempty"`
	Effect            string `json:"effect,omitempty"`
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}

type TopologySpreadConstraintConfig struct {
	MaxSkew           int32  `json:"maxSkew"`
	TopologyKey       string `json:"topologyKey"`
	WhenUnsatisfiable string `json:"whenUnsatisfiable"`
}

func (c PodPlacementConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *PodPlacementConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan PodPlacementConfig: unsupported type")
	}
	return json.Unmarshal(bytes, c)
}

type PDBConfig struct {
	Kind   string `json:"kind"`
	Amount string `json:"value"`
}

func (c PDBConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *PDBConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan PDBConfig: unsupported type")
	}
	return json.Unmarshal(bytes, c)
}

type DeploymentStrategyConfig struct {
	Type          string               `json:"type"`
	RollingUpdate *RollingUpdateConfig `json:"rollingUpdate,omitempty"`
}

type RollingUpdateConfig struct {
	MaxSurge       string `json:"maxSurge,omitempty"`
	MaxUnavailable string `json:"maxUnavailable,omitempty"`
}

func (c DeploymentStrategyConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *DeploymentStrategyConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan DeploymentStrategyConfig: unsupported type")
	}
	return json.Unmarshal(bytes, c)
}
