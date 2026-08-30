ALTER TABLE clients DROP COLUMN IF EXISTS allowed_methods;
ALTER TABLE client_route_attachments DROP COLUMN IF EXISTS enable_header_auth;
DROP TABLE IF EXISTS client_headers;
