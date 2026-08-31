package cluster

import (
	"context"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

// ProjectCredentials is the only thing the cluster layer needs from the rest of
// the application: the connection details for a project's Kubernetes API, and
// the ability to decrypt its stored credentials. *services.ProjectService
// satisfies this structurally, which is what keeps services -> cluster a
// one-way dependency.
type ProjectCredentials interface {
	GetByID(id uuid.UUID) (*models.Project, error)
	GetDecryptedToken(id uuid.UUID) (string, error)
	GetDecryptedClientKey(id uuid.UUID) (string, error)
}

// Client performs Kubernetes operations against a project's cluster.
type Client struct {
	creds      ProjectCredentials
	testClient dynamic.Interface // when set, used instead of building per-project clients (tests only)
}

// New creates a cluster client backed by the given credential source.
func New(creds ProjectCredentials) *Client {
	return &Client{creds: creds}
}

// getClient creates a dynamic Kubernetes client for a project
func (s *Client) getClient(projectID uuid.UUID) (dynamic.Interface, error) {
	project, err := s.creds.GetByID(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	var config *rest.Config

	switch project.ConnectionType {
	case "in_cluster":
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
		}

	case "kubeconfig", "api_token", "":
		// Default to api_token behavior for backward compatibility
		config = &rest.Config{
			Host: project.K8sAPIURL,
		}

		// Auth: token or client cert
		if project.K8sTokenEncrypted != "" {
			token, err := s.creds.GetDecryptedToken(projectID)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt token: %w", err)
			}
			config.BearerToken = token
		} else if project.K8sClientCert != "" {
			config.TLSClientConfig.CertData = []byte(project.K8sClientCert)
			// Decrypt client key
			if project.K8sClientKeyEncrypted != "" {
				clientKey, err := s.creds.GetDecryptedClientKey(projectID)
				if err != nil {
					return nil, fmt.Errorf("failed to decrypt client key: %w", err)
				}
				config.TLSClientConfig.KeyData = []byte(clientKey)
			}
		}

		// TLS verification
		if project.K8sTLSSkipVerify {
			config.TLSClientConfig.Insecure = true
		} else if project.K8sCACert != "" {
			config.TLSClientConfig.CAData = []byte(project.K8sCACert)
		}
		// else: use system CA bundle (default behavior)

	default:
		return nil, fmt.Errorf("unknown connection type: %s", project.ConnectionType)
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return client, nil
}

// getClientDirect creates a dynamic Kubernetes client directly from URL and token
func (s *Client) getClientDirect(apiURL, token string) (dynamic.Interface, error) {
	config := &rest.Config{
		Host:        apiURL,
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true, // TODO: Make this configurable
		},
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return client, nil
}

// joinStrings joins strings with a separator
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

// updateUnstructuredWithRetry performs a read-modify-write update of the
// object called name, retrying on a Kubernetes optimistic-concurrency
// conflict.
//
// Kubernetes rejects an Update whose resourceVersion is stale
// ("Operation cannot be fulfilled ...: the object has been modified"),
// which is what happens whenever two operations touch the same object
// concurrently. Objects scoped to a DOMAIN rather than a route --
// the ClientTrafficPolicy, the mTLS CA Secret, a client's API-key Secret
// -- are exactly that: every mTLS CA add/remove, TLS change and
// client-connection change on one domain rewrites the same object, so two
// admins working on a domain at the same time (or, in the e2e suite, two
// parallel tests) reliably produced a user-visible 400. Re-reading the
// current resourceVersion and retrying resolves it; desired is rebuilt
// from the caller's config each attempt, so the last writer still wins,
// it just no longer fails spuriously.
func updateUnstructuredWithRetry(ctx context.Context, ri dynamic.ResourceInterface, name string, desired *unstructured.Unstructured) error {
	// desired is stamped in place rather than deep-copied per attempt.
	// Unstructured.DeepCopy goes through runtime.DeepCopyJSONValue, which
	// panics on any value that is not a JSON-native type -- and these
	// objects are hand-built maps that can still hold a []string
	// (see kubernetes.BuildClientTrafficPolicy's TLS ciphers). Only resourceVersion
	// and uid change between attempts, so mutating the caller's object is
	// both sufficient and what the pre-retry code already did.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := ri.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		desired.SetResourceVersion(existing.GetResourceVersion())
		desired.SetUID(existing.GetUID())
		_, err = ri.Update(ctx, desired, metav1.UpdateOptions{})
		return err
	})
}
