-- Remove enable_api_key from client_route_attachments
ALTER TABLE client_route_attachments DROP COLUMN IF EXISTS enable_api_key;

-- Remove API key fields from clients table
ALTER TABLE clients DROP COLUMN IF EXISTS api_key_created_by;
ALTER TABLE clients DROP COLUMN IF EXISTS api_key_created_at;
ALTER TABLE clients DROP COLUMN IF EXISTS client_id_header_name;
ALTER TABLE clients DROP COLUMN IF EXISTS api_key_header_name;
ALTER TABLE clients DROP COLUMN IF EXISTS api_key_prefix;
ALTER TABLE clients DROP COLUMN IF EXISTS api_key_encrypted;
ALTER TABLE clients DROP COLUMN IF EXISTS api_key_hash;
ALTER TABLE clients DROP COLUMN IF EXISTS api_key_enabled;
