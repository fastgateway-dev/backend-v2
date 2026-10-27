package services

import (
	"errors"
	"fmt"
	"net/url"
)

// ErrInvalidEndpointURL is returned when a project's Kubernetes API URL or
// metrics endpoint is not an http(s) URL with a host.
var ErrInvalidEndpointURL = errors.New("invalid endpoint URL")

// ErrCredentialRebindRequired is returned when a project update moves a
// stored credential's endpoint without supplying the credential again.
//
// Stored cluster and metrics credentials are bound to the endpoint they were
// entered for. Without this rule, changing only the URL would make the next
// test-connection or deploy send the stored secret to the new host.
var ErrCredentialRebindRequired = errors.New("credential must be re-entered when its endpoint changes")

// validateEndpointURL accepts http(s) URLs with a host and no userinfo.
func validateEndpointURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%w: %s must be an http or https URL with a host", ErrInvalidEndpointURL, field)
	}
	if u.User != nil {
		return fmt.Errorf("%w: %s must not contain credentials", ErrInvalidEndpointURL, field)
	}
	return nil
}
