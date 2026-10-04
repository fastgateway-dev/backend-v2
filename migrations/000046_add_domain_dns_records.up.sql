CREATE TABLE domain_dns_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID NOT NULL UNIQUE REFERENCES domains(id) ON DELETE CASCADE,
    provider_credential_id UUID NOT NULL REFERENCES dns_provider_credentials(id),
    record_type VARCHAR(16) NOT NULL DEFAULT 'auto',
    ttl INTEGER,
    proxied BOOLEAN NOT NULL DEFAULT FALSE,
    resolved_target TEXT,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    endpoint_name TEXT,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_domain_dns_records_credential ON domain_dns_records(provider_credential_id);
