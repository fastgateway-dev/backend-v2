CREATE TABLE certificate_export_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    managed_certificate_id UUID NOT NULL REFERENCES managed_certificates(id) ON DELETE CASCADE,
    granted_to UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_certificate_export_grants_cert ON certificate_export_grants(managed_certificate_id);
