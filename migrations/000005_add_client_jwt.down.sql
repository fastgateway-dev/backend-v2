-- Drop the index first
DROP INDEX IF EXISTS idx_client_route_attachments_enable_jwt;

-- Remove enable_jwt from client_route_attachments
ALTER TABLE client_route_attachments DROP COLUMN IF EXISTS enable_jwt;

-- Remove JWT fields from clients table
ALTER TABLE clients DROP COLUMN IF EXISTS jwt_created_by;
ALTER TABLE clients DROP COLUMN IF EXISTS jwt_created_at;
ALTER TABLE clients DROP COLUMN IF EXISTS jwt_claim_to_headers;
ALTER TABLE clients DROP COLUMN IF EXISTS jwt_required_claims;
ALTER TABLE clients DROP COLUMN IF EXISTS jwt_audiences;
ALTER TABLE clients DROP COLUMN IF EXISTS jwt_jwks_url;
ALTER TABLE clients DROP COLUMN IF EXISTS jwt_issuer;
ALTER TABLE clients DROP COLUMN IF EXISTS jwt_enabled;
