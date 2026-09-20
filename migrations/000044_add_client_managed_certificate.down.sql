DROP INDEX IF EXISTS idx_clients_managed_certificate_id;
ALTER TABLE clients DROP COLUMN IF EXISTS managed_certificate_id;
