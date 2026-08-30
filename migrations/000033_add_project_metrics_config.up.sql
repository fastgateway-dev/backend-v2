ALTER TABLE projects ADD COLUMN metrics_endpoint_url VARCHAR(1024) NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN metrics_auth_type VARCHAR(32) NOT NULL DEFAULT 'none';
ALTER TABLE projects ADD COLUMN metrics_username VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN metrics_password_encrypted TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN metrics_token_encrypted TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN metrics_tls_skip_verify BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE projects ADD COLUMN metrics_ca_cert TEXT NOT NULL DEFAULT '';
