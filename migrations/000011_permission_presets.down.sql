-- 1. Add permissions column back to project_team_roles
ALTER TABLE project_team_roles ADD COLUMN permissions TEXT[] NOT NULL DEFAULT '{}';

-- 2. Restore permissions from presets (union of all assigned presets)
UPDATE project_team_roles ptr
SET permissions = (
    SELECT ARRAY(
        SELECT DISTINCT unnest(pp.permissions)
        FROM project_team_presets ptp
        JOIN permission_presets pp ON pp.id = ptp.preset_id
        WHERE ptp.project_team_role_id = ptr.id
        ORDER BY 1
    )
);

-- 3. Drop junction table
DROP TABLE IF EXISTS project_team_presets;

-- 4. Drop presets table
DROP TABLE IF EXISTS permission_presets;
