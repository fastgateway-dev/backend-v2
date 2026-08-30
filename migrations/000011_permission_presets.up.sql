-- 1. Create permission_presets table
CREATE TABLE permission_presets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    permissions TEXT[] NOT NULL,
    is_builtin BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, name)
);

CREATE INDEX idx_permission_presets_project ON permission_presets(project_id);

-- 2. Create project_team_presets junction table
CREATE TABLE project_team_presets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_team_role_id UUID NOT NULL REFERENCES project_team_roles(id) ON DELETE CASCADE,
    preset_id UUID NOT NULL REFERENCES permission_presets(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(project_team_role_id, preset_id)
);

CREATE INDEX idx_project_team_presets_ptr ON project_team_presets(project_team_role_id);
CREATE INDEX idx_project_team_presets_preset ON project_team_presets(preset_id);

-- 3. Seed built-in presets for each existing project
INSERT INTO permission_presets (project_id, name, description, permissions, is_builtin)
SELECT
    p.id,
    'Viewer',
    'Read-only access to routes, clients, and domains',
    ARRAY['route.view', 'client.view', 'domain.view'],
    true
FROM projects p;

INSERT INTO permission_presets (project_id, name, description, permissions, is_builtin)
SELECT
    p.id,
    'Editor',
    'Can create and edit routes, clients, and domains',
    ARRAY['route.view', 'route.create', 'route.edit', 'route.delete', 'route.deploy',
          'client.view', 'client.create', 'client.edit', 'client.manage_ip', 'client.manage_apikey', 'client.manage_jwt', 'client.attach', 'client.detach',
          'domain.view', 'domain.create', 'domain.edit'],
    true
FROM projects p;

INSERT INTO permission_presets (project_id, name, description, permissions, is_builtin)
SELECT
    p.id,
    'Approver',
    'Can approve or reject route and client changes',
    ARRAY['route.view', 'route.approve', 'client.view', 'client.approve', 'domain.view'],
    true
FROM projects p;

INSERT INTO permission_presets (project_id, name, description, permissions, is_builtin)
SELECT
    p.id,
    'Admin',
    'Full project administration access',
    ARRAY['route.view', 'route.create', 'route.edit', 'route.delete', 'route.deploy', 'route.approve',
          'client.view', 'client.create', 'client.edit', 'client.delete', 'client.manage_ip', 'client.manage_apikey', 'client.manage_jwt', 'client.attach', 'client.detach', 'client.approve',
          'domain.view', 'domain.create', 'domain.edit', 'domain.delete',
          'project.settings', 'project.teams', 'project.approval_policy'],
    true
FROM projects p;

-- 4. Migrate existing project_team_roles.permissions to preset assignments
-- Match exact permissions to built-in presets, or create custom presets

-- First, link teams that match Viewer preset exactly
INSERT INTO project_team_presets (project_team_role_id, preset_id)
SELECT ptr.id, pp.id
FROM project_team_roles ptr
JOIN permission_presets pp ON pp.project_id = ptr.project_id AND pp.name = 'Viewer'
WHERE ptr.permissions = ARRAY['route.view', 'client.view', 'domain.view'];

-- Link teams that match Editor preset exactly
INSERT INTO project_team_presets (project_team_role_id, preset_id)
SELECT ptr.id, pp.id
FROM project_team_roles ptr
JOIN permission_presets pp ON pp.project_id = ptr.project_id AND pp.name = 'Editor'
WHERE ptr.permissions @> ARRAY['route.view', 'route.create', 'route.edit', 'route.delete', 'route.deploy',
          'client.view', 'client.create', 'client.edit', 'client.manage_ip', 'client.manage_apikey', 'client.manage_jwt', 'client.attach', 'client.detach',
          'domain.view', 'domain.create', 'domain.edit']
  AND ARRAY['route.view', 'route.create', 'route.edit', 'route.delete', 'route.deploy',
          'client.view', 'client.create', 'client.edit', 'client.manage_ip', 'client.manage_apikey', 'client.manage_jwt', 'client.attach', 'client.detach',
          'domain.view', 'domain.create', 'domain.edit'] @> ptr.permissions
  AND NOT EXISTS (SELECT 1 FROM project_team_presets ptp WHERE ptp.project_team_role_id = ptr.id);

-- Link teams that match Approver preset exactly
INSERT INTO project_team_presets (project_team_role_id, preset_id)
SELECT ptr.id, pp.id
FROM project_team_roles ptr
JOIN permission_presets pp ON pp.project_id = ptr.project_id AND pp.name = 'Approver'
WHERE ptr.permissions @> ARRAY['route.view', 'route.approve', 'client.view', 'client.approve', 'domain.view']
  AND ARRAY['route.view', 'route.approve', 'client.view', 'client.approve', 'domain.view'] @> ptr.permissions
  AND NOT EXISTS (SELECT 1 FROM project_team_presets ptp WHERE ptp.project_team_role_id = ptr.id);

-- Link teams that match Admin preset exactly (all 23 permissions)
INSERT INTO project_team_presets (project_team_role_id, preset_id)
SELECT ptr.id, pp.id
FROM project_team_roles ptr
JOIN permission_presets pp ON pp.project_id = ptr.project_id AND pp.name = 'Admin'
WHERE array_length(ptr.permissions, 1) = 23
  AND NOT EXISTS (SELECT 1 FROM project_team_presets ptp WHERE ptp.project_team_role_id = ptr.id);

-- For any remaining unmatched teams, create a custom preset and link it
INSERT INTO permission_presets (project_id, name, description, permissions, is_builtin)
SELECT DISTINCT ON (ptr.id)
    ptr.project_id,
    'Custom-' || SUBSTRING(ptr.id::text, 1, 8),
    'Migrated custom permissions',
    ptr.permissions,
    false
FROM project_team_roles ptr
WHERE NOT EXISTS (SELECT 1 FROM project_team_presets ptp WHERE ptp.project_team_role_id = ptr.id);

-- Link the custom presets
INSERT INTO project_team_presets (project_team_role_id, preset_id)
SELECT ptr.id, pp.id
FROM project_team_roles ptr
JOIN permission_presets pp ON pp.project_id = ptr.project_id
    AND pp.name = 'Custom-' || SUBSTRING(ptr.id::text, 1, 8)
WHERE NOT EXISTS (SELECT 1 FROM project_team_presets ptp WHERE ptp.project_team_role_id = ptr.id);

-- 5. Drop permissions column from project_team_roles
ALTER TABLE project_team_roles DROP COLUMN permissions;
