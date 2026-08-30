DROP INDEX IF EXISTS idx_client_route_attachments_ext_auth;
ALTER TABLE client_route_attachments DROP COLUMN IF EXISTS ext_auth;
