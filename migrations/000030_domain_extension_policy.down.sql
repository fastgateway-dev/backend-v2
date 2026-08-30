ALTER TABLE envoy_extension_policies DROP CONSTRAINT IF EXISTS chk_envoy_ext_policy_route_or_domain;
DROP INDEX IF EXISTS idx_envoy_extension_policies_domain_id_unique;
DROP INDEX IF EXISTS idx_envoy_extension_policies_domain_id;
ALTER TABLE envoy_extension_policies DROP COLUMN IF EXISTS domain_id;
-- Restore original unique index
DROP INDEX IF EXISTS idx_envoy_extension_policies_route_id_unique;
DELETE FROM envoy_extension_policies WHERE route_id IS NULL;
ALTER TABLE envoy_extension_policies ALTER COLUMN route_id SET NOT NULL;
CREATE UNIQUE INDEX idx_envoy_extension_policies_route_id ON envoy_extension_policies(route_id);
