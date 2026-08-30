package services

import (
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// TopologyStatus is the visual status used by the topology views.
type TopologyStatus string

const (
	TopologyStatusDeployed TopologyStatus = "deployed"
	TopologyStatusPending  TopologyStatus = "pending"
	TopologyStatusFailed   TopologyStatus = "failed"
	TopologyStatusDraft    TopologyStatus = "draft"
)

// MapRouteStatus maps a RouteStatus to a TopologyStatus.
func MapRouteStatus(s models.RouteStatus) TopologyStatus {
	switch s {
	case models.RouteStatusActive:
		return TopologyStatusDeployed
	case models.RouteStatusApproved,
		models.RouteStatusPendingCreate,
		models.RouteStatusPendingUpdate,
		models.RouteStatusPendingDelete,
		models.RouteStatusPendingDeploy:
		return TopologyStatusPending
	case models.RouteStatusRejected:
		return TopologyStatusFailed
	default:
		return TopologyStatusDraft
	}
}

// MapAttachmentStatus maps an AttachmentStatus to a TopologyStatus.
func MapAttachmentStatus(s models.AttachmentStatus) TopologyStatus {
	switch s {
	case models.AttachmentStatusActive:
		return TopologyStatusDeployed
	case models.AttachmentStatusApproved,
		models.AttachmentStatusPendingAttach,
		models.AttachmentStatusPendingUpdate,
		models.AttachmentStatusPendingDetach:
		return TopologyStatusPending
	case models.AttachmentStatusRejected:
		return TopologyStatusFailed
	default:
		return TopologyStatusDraft
	}
}

// MapGatewayStatus maps cached K8s readiness to TopologyStatus.
//
//	ready=true                                  → Deployed
//	reason contains "error" or "failed" (ci)    → Failed
//	reason is empty                             → Draft
//	any other non-empty reason                  → Pending (catch-all)
func MapGatewayStatus(ready bool, reason string) TopologyStatus {
	if ready {
		return TopologyStatusDeployed
	}
	r := strings.ToLower(reason)
	if r == "" {
		return TopologyStatusDraft
	}
	if strings.Contains(r, "error") || strings.Contains(r, "failed") {
		return TopologyStatusFailed
	}
	return TopologyStatusPending
}

// AggregateStatus folds child statuses using the spec ordering
// failed > pending > draft > deployed.
// An empty input returns Draft (no children → not yet submitted).
func AggregateStatus(in []TopologyStatus) TopologyStatus {
	if len(in) == 0 {
		return TopologyStatusDraft
	}
	priority := map[TopologyStatus]int{
		TopologyStatusFailed:   3,
		TopologyStatusPending:  2,
		TopologyStatusDraft:    1,
		TopologyStatusDeployed: 0,
	}
	best := TopologyStatusDeployed
	for _, s := range in {
		if priority[s] > priority[best] {
			best = s
		}
	}
	return best
}
