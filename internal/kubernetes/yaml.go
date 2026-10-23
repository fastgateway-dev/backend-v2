package kubernetes

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// MarshalRouteYAML renders a typed Gateway API route object (HTTPRoute,
// GRPCRoute, TCPRoute, UDPRoute) as YAML for previews and manifests.
//
// Marshalling the typed struct directly emits a server-populated `status`
// block (`status: parents: null`) and a null `metadata.creationTimestamp`,
// neither of which belongs in a manifest. Converting through unstructured
// lets both be removed before marshalling.
func MarshalRouteYAML(obj any) ([]byte, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("convert route to unstructured: %w", err)
	}
	delete(m, "status")
	if meta, ok := m["metadata"].(map[string]interface{}); ok {
		if v, present := meta["creationTimestamp"]; present && v == nil {
			delete(meta, "creationTimestamp")
		}
	}
	return yaml.Marshal(m)
}
