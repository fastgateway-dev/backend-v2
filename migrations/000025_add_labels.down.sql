DROP INDEX IF EXISTS idx_routes_labels;
DROP INDEX IF EXISTS idx_domains_labels;
DROP INDEX IF EXISTS idx_projects_labels;

ALTER TABLE routes DROP COLUMN IF EXISTS labels;
ALTER TABLE domains DROP COLUMN IF EXISTS labels;
ALTER TABLE projects DROP COLUMN IF EXISTS labels;
