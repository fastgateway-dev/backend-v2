-- Add mTLS columns to clients table
ALTER TABLE clients ADD COLUMN mtls_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE clients ADD COLUMN mtls_ca_name VARCHAR(255);
ALTER TABLE clients ADD COLUMN mtls_ca_secret VARCHAR(255);
ALTER TABLE clients ADD COLUMN mtls_ca_secret_key VARCHAR(255) DEFAULT 'ca.crt';
ALTER TABLE clients ADD COLUMN mtls_sans JSONB;
ALTER TABLE clients ADD COLUMN mtls_hashes JSONB;
ALTER TABLE clients ADD COLUMN mtls_created_at TIMESTAMP;
ALTER TABLE clients ADD COLUMN mtls_created_by UUID REFERENCES users(id);

-- Create index for querying mTLS-enabled clients
CREATE INDEX idx_clients_mtls_enabled ON clients(mtls_enabled) WHERE mtls_enabled = TRUE;
