package kubernetes

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// HTTPRouteFilterConfig represents Envoy Gateway HTTPRouteFilter configuration for Direct Response
type HTTPRouteFilterConfig struct {
	Name           string
	Namespace      string
	GatewayID      string // Domain UUID for labeling
	RouteID        string // Route UUID for labeling
	DirectResponse *DirectResponseFilterConfig
}

// DirectResponseFilterConfig represents direct response configuration for HTTPRouteFilter
type DirectResponseFilterConfig struct {
	StatusCode  int
	ContentType string
	Body        *DirectResponseBodyFilterConfig
}

// DirectResponseBodyFilterConfig represents body configuration for HTTPRouteFilter
type DirectResponseBodyFilterConfig struct {
	Type     string                  // "Inline" or "ValueRef"
	Inline   string                  // For Inline type
	ValueRef *DirectResponseValueRef // For ValueRef type
}

// DirectResponseValueRef represents a reference to a ConfigMap
type DirectResponseValueRef struct {
	Group string
	Kind  string
	Name  string
}

// DirectResponseConfigMapConfig represents ConfigMap configuration for Direct Response body
type DirectResponseConfigMapConfig struct {
	Name        string
	Namespace   string
	GatewayID   string // Domain UUID for labeling
	RouteID     string // Route UUID for labeling
	BodyContent string // The response body content
}

// BuildHTTPRouteFilter builds an HTTPRouteFilter resource for Direct Response
func BuildHTTPRouteFilter(config *HTTPRouteFilterConfig) *unstructured.Unstructured {
	spec := map[string]interface{}{}

	if config.DirectResponse != nil {
		dr := map[string]interface{}{
			"statusCode": config.DirectResponse.StatusCode,
		}

		if config.DirectResponse.ContentType != "" {
			dr["contentType"] = config.DirectResponse.ContentType
		}

		if config.DirectResponse.Body != nil {
			body := map[string]interface{}{
				"type": config.DirectResponse.Body.Type,
			}
			if config.DirectResponse.Body.Type == "Inline" && config.DirectResponse.Body.Inline != "" {
				body["inline"] = config.DirectResponse.Body.Inline
			}
			if config.DirectResponse.Body.Type == "ValueRef" && config.DirectResponse.Body.ValueRef != nil {
				body["valueRef"] = map[string]interface{}{
					"group": config.DirectResponse.Body.ValueRef.Group,
					"kind":  config.DirectResponse.Body.ValueRef.Kind,
					"name":  config.DirectResponse.Body.ValueRef.Name,
				}
			}
			dr["body"] = body
		}

		spec["directResponse"] = dr
	}

	httpRouteFilter := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "HTTPRouteFilter",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    ForRouteInterface(config.GatewayID, config.RouteID),
			},
			"spec": spec,
		},
	}

	return httpRouteFilter
}

// BuildDirectResponseConfigMap builds a ConfigMap for Direct Response body
func BuildDirectResponseConfigMap(config *DirectResponseConfigMapConfig) *unstructured.Unstructured {
	configMap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    ForRouteInterface(config.GatewayID, config.RouteID),
			},
			"data": map[string]interface{}{
				"response.body": config.BodyContent,
			},
		},
	}

	return configMap
}
