-- Client header entries (mirrors client_ip_addresses)
CREATE TABLE client_headers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    values JSONB NOT NULL,
    description VARCHAR(255),
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_client_headers_client_id ON client_headers(client_id);

-- Add header auth field to client attachments
ALTER TABLE client_route_attachments ADD COLUMN enable_header_auth BOOLEAN NOT NULL DEFAULT false;

-- Add allowed methods to clients (client-level, not per-attachment)
ALTER TABLE clients ADD COLUMN allowed_methods JSONB;
