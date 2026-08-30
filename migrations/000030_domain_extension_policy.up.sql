-- Make route_id nullable (was NOT NULL) so domain-level policies can omit it
ALTER TABLE envoy_extension_policies ALTER COLUMN route_id DROP NOT NULL;

-- Fix existing unique index on route_id: must be partial to allow multiple NULLs
DROP INDEX IF EXISTS idx_envoy_extension_policies_route_id;
CREATE UNIQUE INDEX idx_envoy_extension_policies_route_id_unique ON envoy_extension_policies(route_id) WHERE route_id IS NOT NULL;

-- Add domain_id column
ALTER TABLE envoy_extension_policies ADD COLUMN domain_id UUID REFERENCES domains(id) ON DELETE CASCADE;

-- Add index on domain_id
CREATE INDEX idx_envoy_extension_policies_domain_id ON envoy_extension_policies(domain_id);

-- Add unique constraint on domain_id (only one extension policy per domain)
CREATE UNIQUE INDEX idx_envoy_extension_policies_domain_id_unique ON envoy_extension_policies(domain_id) WHERE domain_id IS NOT NULL;

-- Add constraint: either route_id or domain_id must be set, not both
ALTER TABLE envoy_extension_policies ADD CONSTRAINT chk_envoy_ext_policy_route_or_domain
    CHECK (
        (route_id IS NOT NULL AND domain_id IS NULL) OR
        (route_id IS NULL AND domain_id IS NOT NULL)
    );
