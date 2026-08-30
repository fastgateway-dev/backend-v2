-- Add connection type column
ALTER TABLE projects ADD COLUMN connection_type VARCHAR(20) NOT NULL DEFAULT 'api_token';

-- Add TLS-related columns
ALTER TABLE projects ADD COLUMN k8s_ca_cert TEXT;
ALTER TABLE projects ADD COLUMN k8s_tls_skip_verify BOOLEAN NOT NULL DEFAULT true;

-- Add client certificate columns (for kubeconfig cert-based auth)
ALTER TABLE projects ADD COLUMN k8s_client_cert TEXT;
ALTER TABLE projects ADD COLUMN k8s_client_key_encrypted TEXT;

-- Make existing columns nullable (for in_cluster type)
ALTER TABLE projects ALTER COLUMN k8s_api_url DROP NOT NULL;
ALTER TABLE projects ALTER COLUMN k8s_token_encrypted DROP NOT NULL;
