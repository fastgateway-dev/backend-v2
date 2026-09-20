DROP INDEX IF EXISTS idx_domains_managed_certificate_id;
ALTER TABLE domains DROP COLUMN IF EXISTS managed_certificate_id;
