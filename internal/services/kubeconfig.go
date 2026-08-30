package services

import (
	"errors"
	"os"

	"k8s.io/client-go/tools/clientcmd"
)

// ParsedKubeconfig contains extracted credentials from a kubeconfig file
type ParsedKubeconfig struct {
	APIUrl     string
	CACert     []byte
	Token      string
	ClientCert []byte
	ClientKey  []byte
	SkipTLS    bool
}

// ParseKubeconfig parses and validates a kubeconfig YAML string
func ParseKubeconfig(kubeconfigData string) (*ParsedKubeconfig, error) {
	config, err := clientcmd.Load([]byte(kubeconfigData))
	if err != nil {
		return nil, errors.New("invalid kubeconfig format")
	}

	if config.CurrentContext == "" {
		return nil, errors.New("kubeconfig has no current-context set")
	}

	ctx, ok := config.Contexts[config.CurrentContext]
	if !ok {
		return nil, errors.New("current-context not found in kubeconfig")
	}

	cluster, ok := config.Clusters[ctx.Cluster]
	if !ok {
		return nil, errors.New("cluster not found in kubeconfig")
	}

	authInfo, ok := config.AuthInfos[ctx.AuthInfo]
	if !ok {
		return nil, errors.New("user not found in kubeconfig")
	}

	// Reject exec-based auth
	if authInfo.Exec != nil {
		return nil, errors.New("exec-based authentication (EKS, GKE, AKS) is not supported. Please use a kubeconfig with static token or client certificate")
	}

	// Reject auth provider (another form of dynamic auth)
	if authInfo.AuthProvider != nil {
		return nil, errors.New("auth-provider based authentication is not supported. Please use a kubeconfig with static token or client certificate")
	}

	// Check for file-path based credentials (not supported - we can't read files on the server)
	if authInfo.TokenFile != "" {
		return nil, errors.New("kubeconfig with tokenFile is not supported. Please use a kubeconfig with an embedded token")
	}
	if authInfo.ClientCertificate != "" || authInfo.ClientKey != "" {
		return nil, errors.New("kubeconfig with file-path certificates is not supported. Please use a kubeconfig with embedded certificate data (use 'kubectl config view --flatten')")
	}

	// Check for valid embedded credentials
	hasToken := authInfo.Token != ""
	hasCert := len(authInfo.ClientCertificateData) > 0
	hasKey := len(authInfo.ClientKeyData) > 0

	if !hasToken && !(hasCert && hasKey) {
		return nil, errors.New("kubeconfig must contain an embedded token or client certificate/key pair")
	}

	parsed := &ParsedKubeconfig{
		APIUrl:  cluster.Server,
		SkipTLS: cluster.InsecureSkipTLSVerify,
	}

	// Extract CA cert
	if len(cluster.CertificateAuthorityData) > 0 {
		parsed.CACert = cluster.CertificateAuthorityData
	}

	// Extract token
	if authInfo.Token != "" {
		parsed.Token = authInfo.Token
	}

	// Extract client cert/key
	if len(authInfo.ClientCertificateData) > 0 {
		parsed.ClientCert = authInfo.ClientCertificateData
	}
	if len(authInfo.ClientKeyData) > 0 {
		parsed.ClientKey = authInfo.ClientKeyData
	}

	return parsed, nil
}

// IsRunningInCluster checks if the application is running inside a Kubernetes cluster
func IsRunningInCluster() bool {
	// Check for service account token file
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
		return true
	}

	// Also check KUBERNETES_SERVICE_HOST env var
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}

	return false
}
