package clients

import (
	"context"

	"github.com/google/uuid"
)

type SecretWriter interface {
	CreateOrUpdateSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, data map[string][]byte) error
	DeleteSecret(ctx context.Context, projectID uuid.UUID, namespace, name string) error
}

type APIKeySecretDeleter interface {
	DeleteAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) error
}
