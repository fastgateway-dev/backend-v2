package handlers

import "strings"

// parseLabelsFilter parses a comma-separated "key=value" labels query parameter
func parseLabelsFilter(labelsParam string) map[string]string {
	if labelsParam == "" {
		return nil
	}
	labels := make(map[string]string)
	for _, pair := range strings.Split(labelsParam, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if key != "" {
				labels[key] = value
			}
		}
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}
