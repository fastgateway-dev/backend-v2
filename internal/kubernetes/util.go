package kubernetes

// stringSliceToInterfaceSlice converts []string to []interface{} for unstructured objects
// Kubernetes unstructured library cannot deep copy []string directly
func stringSliceToInterfaceSlice(s []string) []interface{} {
	result := make([]interface{}, len(s))
	for i, v := range s {
		result[i] = v
	}
	return result
}
