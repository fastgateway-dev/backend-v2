-- Delete non-api_token projects (they won't work after rollback)
DELETE FROM projects WHERE connection_type != 'api_token';

-- Drop new columns
ALTER TABLE projects DROP COLUMN connection_type;
ALTER TABLE projects DROP COLUMN k8s_ca_cert;
ALTER TABLE projects DROP COLUMN k8s_tls_skip_verify;
ALTER TABLE projects DROP COLUMN k8s_client_cert;
ALTER TABLE projects DROP COLUMN k8s_client_key_encrypted;

-- Restore NOT NULL constraints
ALTER TABLE projects ALTER COLUMN k8s_api_url SET NOT NULL;
ALTER TABLE projects ALTER COLUMN k8s_token_encrypted SET NOT NULL;
