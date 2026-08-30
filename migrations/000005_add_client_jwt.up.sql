-- Add JWT fields to clients table
ALTER TABLE clients ADD COLUMN jwt_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE clients ADD COLUMN jwt_issuer VARCHAR(500);
ALTER TABLE clients ADD COLUMN jwt_jwks_url VARCHAR(500);
ALTER TABLE clients ADD COLUMN jwt_audiences JSONB;
ALTER TABLE clients ADD COLUMN jwt_required_claims JSONB;
ALTER TABLE clients ADD COLUMN jwt_claim_to_headers JSONB;
ALTER TABLE clients ADD COLUMN jwt_created_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE clients ADD COLUMN jwt_created_by UUID REFERENCES users(id);

-- Add enable_jwt to client_route_attachments
ALTER TABLE client_route_attachments ADD COLUMN enable_jwt BOOLEAN NOT NULL DEFAULT false;

-- Add partial index for enable_jwt to optimize queries filtering by JWT-enabled attachments
CREATE INDEX idx_client_route_attachments_enable_jwt ON client_route_attachments(client_id) WHERE enable_jwt = true;
