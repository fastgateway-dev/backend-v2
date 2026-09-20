ALTER TABLE clients ADD COLUMN managed_certificate_id UUID REFERENCES managed_certificates(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX idx_clients_managed_certificate_id ON clients(managed_certificate_id) WHERE managed_certificate_id IS NOT NULL;
