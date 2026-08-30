DROP TABLE IF EXISTS envoy_extension_policies;

-- Remove extension policy snapshot columns from approval_requests table
ALTER TABLE approval_requests
DROP COLUMN IF EXISTS envoy_extension_policy_snapshot,
DROP COLUMN IF EXISTS previous_envoy_extension_policy;
