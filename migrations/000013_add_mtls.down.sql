DROP INDEX IF EXISTS idx_clients_mtls_enabled;
ALTER TABLE clients DROP COLUMN IF EXISTS mtls_enabled;
ALTER TABLE clients DROP COLUMN IF EXISTS mtls_ca_name;
ALTER TABLE clients DROP COLUMN IF EXISTS mtls_ca_secret;
ALTER TABLE clients DROP COLUMN IF EXISTS mtls_ca_secret_key;
ALTER TABLE clients DROP COLUMN IF EXISTS mtls_sans;
ALTER TABLE clients DROP COLUMN IF EXISTS mtls_hashes;
ALTER TABLE clients DROP COLUMN IF EXISTS mtls_created_at;
ALTER TABLE clients DROP COLUMN IF EXISTS mtls_created_by;
