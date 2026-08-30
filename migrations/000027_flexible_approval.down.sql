DROP INDEX IF EXISTS idx_approval_stage_reviews_stage_id;
DROP TABLE IF EXISTS approval_stage_reviews;
ALTER TABLE approval_stages DROP COLUMN IF EXISTS min_approvers;
ALTER TABLE projects DROP COLUMN IF EXISTS self_approval_allowed;
ALTER TABLE projects DROP COLUMN IF EXISTS approval_enabled;
