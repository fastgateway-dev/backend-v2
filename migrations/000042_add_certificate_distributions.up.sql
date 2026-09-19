CREATE TABLE certificate_distributions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    managed_certificate_id UUID NOT NULL UNIQUE REFERENCES managed_certificates(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    last_pushed_fingerprint TEXT,
    message TEXT,
    last_synced_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_certificate_distributions_project ON certificate_distributions(project_id);
