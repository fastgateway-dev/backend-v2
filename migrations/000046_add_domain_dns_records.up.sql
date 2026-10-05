CREATE TABLE dns_hosted_zones (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    provider_credential_id UUID NOT NULL REFERENCES dns_provider_credentials(id),
    provider_zone_id TEXT,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_dns_hosted_zones_credential ON dns_hosted_zones(provider_credential_id);

CREATE TABLE domain_dns_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID NOT NULL UNIQUE REFERENCES domains(id) ON DELETE CASCADE,
    hosted_zone_id UUID NOT NULL REFERENCES dns_hosted_zones(id),
    record_type VARCHAR(16) NOT NULL DEFAULT 'auto',
    ttl INTEGER,
    proxied BOOLEAN NOT NULL DEFAULT FALSE,
    resolved_target TEXT,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_domain_dns_records_zone ON domain_dns_records(hosted_zone_id);
