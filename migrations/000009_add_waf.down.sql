DROP TABLE IF EXISTS waf_policies;

-- Remove WAF policy snapshot columns from approval_requests table
ALTER TABLE approval_requests
DROP COLUMN IF EXISTS waf_policy_snapshot,
DROP COLUMN IF EXISTS previous_waf_policy;
