package streamplan_test

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

func unstructuredSlice(obj map[string]interface{}, fields ...string) ([]interface{}, bool, error) {
	return unstructured.NestedSlice(obj, fields...)
}
