package services_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/stretchr/testify/assert"
)

func TestMapRouteStatus(t *testing.T) {
	cases := []struct {
		in   models.RouteStatus
		want services.TopologyStatus
	}{
		{models.RouteStatusActive, services.TopologyStatusDeployed},
		{models.RouteStatusApproved, services.TopologyStatusPending},
		{models.RouteStatusPendingCreate, services.TopologyStatusPending},
		{models.RouteStatusPendingUpdate, services.TopologyStatusPending},
		{models.RouteStatusPendingDelete, services.TopologyStatusPending},
		{models.RouteStatusPendingDeploy, services.TopologyStatusPending},
		{models.RouteStatusRejected, services.TopologyStatusFailed},
		{models.RouteStatus(""), services.TopologyStatusDraft},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, services.MapRouteStatus(c.in), "in=%s", c.in)
	}
}

func TestMapAttachmentStatus(t *testing.T) {
	cases := []struct {
		in   models.AttachmentStatus
		want services.TopologyStatus
	}{
		{models.AttachmentStatusActive, services.TopologyStatusDeployed},
		{models.AttachmentStatusApproved, services.TopologyStatusPending},
		{models.AttachmentStatusPendingAttach, services.TopologyStatusPending},
		{models.AttachmentStatusPendingUpdate, services.TopologyStatusPending},
		{models.AttachmentStatusPendingDetach, services.TopologyStatusPending},
		{models.AttachmentStatusRejected, services.TopologyStatusFailed},
		{models.AttachmentStatusRemoved, services.TopologyStatusDraft},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, services.MapAttachmentStatus(c.in), "in=%s", c.in)
	}
}

func TestMapGatewayStatus(t *testing.T) {
	cases := []struct {
		name   string
		ready  bool
		reason string
		want   services.TopologyStatus
	}{
		{"ready", true, "", services.TopologyStatusDeployed},
		{"pending reason", false, "AddressPending", services.TopologyStatusPending},
		{"failed reason", false, "ProgrammingError", services.TopologyStatusFailed},
		{"absent", false, "", services.TopologyStatusDraft},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, services.MapGatewayStatus(c.ready, c.reason))
		})
	}
}

func TestAggregateStatus(t *testing.T) {
	d := services.TopologyStatusDeployed
	p := services.TopologyStatusPending
	f := services.TopologyStatusFailed
	dr := services.TopologyStatusDraft

	cases := []struct {
		name string
		in   []services.TopologyStatus
		want services.TopologyStatus
	}{
		{"empty returns draft", nil, services.TopologyStatusDraft},
		{"single deployed", []services.TopologyStatus{d}, d},
		{"all deployed", []services.TopologyStatus{d, d}, d},
		{"pending beats deployed", []services.TopologyStatus{d, p}, p},
		{"draft beats deployed", []services.TopologyStatus{d, dr}, dr},
		{"pending beats draft", []services.TopologyStatus{dr, p}, p},
		{"pending beats draft and deployed", []services.TopologyStatus{d, p, dr}, p},
		{"failed beats everything", []services.TopologyStatus{d, p, dr, f}, f},
		{"failed beats pending", []services.TopologyStatus{p, f}, f},
		{"failed beats draft", []services.TopologyStatus{dr, f}, f},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, services.AggregateStatus(c.in))
		})
	}
}
