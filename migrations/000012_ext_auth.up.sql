-- Add ext_auth column to client_route_attachments table
ALTER TABLE client_route_attachments
ADD COLUMN IF NOT EXISTS ext_auth JSONB;

-- Add index for querying attachments with ext-auth configured
CREATE INDEX IF NOT EXISTS idx_client_route_attachments_ext_auth
ON client_route_attachments USING GIN (ext_auth)
WHERE ext_auth IS NOT NULL;
