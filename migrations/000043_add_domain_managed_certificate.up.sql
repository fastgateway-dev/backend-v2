ALTER TABLE domains
    ADD COLUMN managed_certificate_id UUID REFERENCES managed_certificates(id) ON DELETE RESTRICT;

CREATE INDEX idx_domains_managed_certificate_id ON domains(managed_certificate_id);
