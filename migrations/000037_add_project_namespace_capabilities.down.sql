DROP INDEX IF EXISTS idx_project_namespaces_capabilities;
ALTER TABLE project_namespaces DROP COLUMN IF EXISTS capabilities;
