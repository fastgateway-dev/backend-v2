-- Add API key fields to clients table
ALTER TABLE clients ADD COLUMN api_key_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE clients ADD COLUMN api_key_hash VARCHAR(255);
ALTER TABLE clients ADD COLUMN api_key_encrypted TEXT;
ALTER TABLE clients ADD COLUMN api_key_prefix VARCHAR(20);
ALTER TABLE clients ADD COLUMN api_key_header_name VARCHAR(100) DEFAULT 'x-api-key';
ALTER TABLE clients ADD COLUMN client_id_header_name VARCHAR(100) DEFAULT 'x-client-id';
ALTER TABLE clients ADD COLUMN api_key_created_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE clients ADD COLUMN api_key_created_by UUID REFERENCES users(id);

-- Add enable_api_key to client_route_attachments
ALTER TABLE client_route_attachments ADD COLUMN enable_api_key BOOLEAN NOT NULL DEFAULT false;
