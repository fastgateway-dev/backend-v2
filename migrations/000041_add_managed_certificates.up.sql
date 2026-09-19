CREATE TABLE managed_certificates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    issuer_id UUID NOT NULL REFERENCES certificate_issuers(id),
    usage VARCHAR(16) NOT NULL,
    config JSONB NOT NULL DEFAULT '{}',
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    fingerprint TEXT,
    not_after TIMESTAMP WITH TIME ZONE,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_managed_certificates_project ON managed_certificates(project_id);
CREATE INDEX idx_managed_certificates_issuer ON managed_certificates(issuer_id);
