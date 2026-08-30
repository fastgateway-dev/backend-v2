package handlers

import (
	"context"
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// KubernetesHandler handles Kubernetes discovery endpoints
type KubernetesHandler struct {
	k8sService services.KubernetesServiceInterface
}

// NewKubernetesHandler creates a new Kubernetes handler
func NewKubernetesHandler(k8sService services.KubernetesServiceInterface) *KubernetesHandler {
	return &KubernetesHandler{
		k8sService: k8sService,
	}
}

// K8sNamespace represents a Kubernetes namespace
type K8sNamespace struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// K8sService represents a Kubernetes service
type K8sService struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Ports     []K8sPort `json:"ports"`
}

// K8sPort represents a Kubernetes service port
type K8sPort struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

// ListNamespaces lists Kubernetes namespaces
func (h *KubernetesHandler) ListNamespaces(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	ctx := context.Background()
	namespaceNames, err := h.k8sService.ListNamespaces(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	namespaces := make([]K8sNamespace, 0, len(namespaceNames))
	for _, name := range namespaceNames {
		namespaces = append(namespaces, K8sNamespace{
			Name:   name,
			Status: "Active",
		})
	}

	c.JSON(http.StatusOK, namespaces)
}

// ListServices lists Kubernetes services in a namespace
func (h *KubernetesHandler) ListServices(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	namespace := c.Param("namespace")

	ctx := context.Background()
	serviceList, err := h.k8sService.ListServices(ctx, projectID, namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	services := make([]K8sService, 0, len(serviceList))
	for _, svc := range serviceList {
		ports := make([]K8sPort, 0)
		if portList, ok := svc["ports"].([]map[string]interface{}); ok {
			for _, p := range portList {
				port := K8sPort{}
				if name, ok := p["name"].(string); ok {
					port.Name = name
				}
				if portNum, ok := p["port"].(int64); ok {
					port.Port = int(portNum)
				}
				if protocol, ok := p["protocol"].(string); ok {
					port.Protocol = protocol
				}
				ports = append(ports, port)
			}
		}

		services = append(services, K8sService{
			Name:      svc["name"].(string),
			Namespace: svc["namespace"].(string),
			Ports:     ports,
		})
	}

	c.JSON(http.StatusOK, services)
}

// ListGatewayClasses lists available GatewayClasses
func (h *KubernetesHandler) ListGatewayClasses(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	ctx := context.Background()
	classes, err := h.k8sService.ListGatewayClasses(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, classes)
}
