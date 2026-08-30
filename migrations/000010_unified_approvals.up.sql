-- 1. Add permissions column to project_team_roles (keep role for migration)
ALTER TABLE project_team_roles ADD COLUMN permissions TEXT[] NOT NULL DEFAULT '{}';

-- 2. Migrate existing roles to permissions
UPDATE project_team_roles SET permissions = ARRAY[
    'route.view', 'client.view', 'domain.view'
] WHERE role = 'viewer';

UPDATE project_team_roles SET permissions = ARRAY[
    'route.view', 'route.create', 'route.edit', 'route.delete', 'route.deploy',
    'client.view', 'client.create', 'client.edit', 'client.manage_ip', 'client.manage_apikey', 'client.manage_jwt', 'client.attach', 'client.detach',
    'domain.view', 'domain.create', 'domain.edit'
] WHERE role = 'editor';

UPDATE project_team_roles SET permissions = ARRAY[
    'route.view', 'route.approve',
    'client.view', 'client.approve',
    'domain.view'
] WHERE role = 'approver';

-- 3. Drop role column
ALTER TABLE project_team_roles DROP COLUMN role;
DROP INDEX IF EXISTS idx_project_team_roles_role;

-- 4. Create unified approvals table
CREATE TABLE approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    entity_type VARCHAR(50) NOT NULL,
    entity_id UUID NOT NULL,
    action VARCHAR(50) NOT NULL,
    config_snapshot JSONB,
    previous_config JSONB,
    submitted_by UUID NOT NULL REFERENCES users(id),
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_approvals_project_status ON approvals(project_id, status);
CREATE INDEX idx_approvals_entity ON approvals(entity_type, entity_id);

-- 5. Create approval_stages table
CREATE TABLE approval_stages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    approval_id UUID NOT NULL REFERENCES approvals(id) ON DELETE CASCADE,
    stage_order INT NOT NULL,
    required_permission VARCHAR(100) NOT NULL,
    required_team_id UUID REFERENCES teams(id),
    reviewed_by UUID REFERENCES users(id),
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    comment TEXT,
    reviewed_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(approval_id, stage_order)
);

CREATE INDEX idx_approval_stages_approval ON approval_stages(approval_id);

-- 6. Create approval_policies table
CREATE TABLE approval_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    entity_type VARCHAR(50) NOT NULL,
    action VARCHAR(50),
    stages JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_approval_policies_project ON approval_policies(project_id);
CREATE UNIQUE INDEX idx_approval_policies_unique ON approval_policies(project_id, entity_type, COALESCE(action, ''));

-- 7. Migrate existing approval_requests to approvals + approval_stages
INSERT INTO approvals (id, project_id, entity_type, entity_id, action, config_snapshot, previous_config, submitted_by, status, created_at)
SELECT
    ar.id,
    d.project_id,
    'route',
    ar.route_id,
    ar.action,
    jsonb_build_object(
        'routeConfig', ar.config_snapshot,
        'securityPolicy', ar.security_policy_snapshot,
        'backendTrafficPolicy', ar.backend_traffic_policy_snapshot,
        'envoyExtensionPolicy', ar.envoy_extension_policy_snapshot,
        'wafPolicy', ar.waf_policy_snapshot
    ),
    jsonb_build_object(
        'routeConfig', ar.previous_config,
        'securityPolicy', ar.previous_security_policy,
        'backendTrafficPolicy', ar.previous_backend_traffic_policy,
        'envoyExtensionPolicy', ar.previous_envoy_extension_policy,
        'wafPolicy', ar.previous_waf_policy
    ),
    ar.submitted_by,
    ar.status,
    ar.created_at
FROM approval_requests ar
JOIN routes r ON r.id = ar.route_id
JOIN domains d ON d.id = r.domain_id;

-- Create single stage for each migrated route approval
INSERT INTO approval_stages (approval_id, stage_order, required_permission, reviewed_by, status, comment, reviewed_at)
SELECT
    ar.id,
    1,
    'route.approve',
    ar.reviewed_by,
    ar.status,
    ar.rejection_comment,
    ar.reviewed_at
FROM approval_requests ar;

-- 8. Migrate client_attachment_approvals to approvals + approval_stages
INSERT INTO approvals (id, project_id, entity_type, entity_id, action, submitted_by, status, created_at)
SELECT
    caa.id,
    d.project_id,
    'client_attachment',
    caa.attachment_id,
    caa.action,
    caa.submitted_by,
    caa.status,
    caa.created_at
FROM client_attachment_approvals caa
JOIN client_route_attachments cra ON cra.id = caa.attachment_id
JOIN routes r ON r.id = cra.route_id
JOIN domains d ON d.id = r.domain_id;

-- Stage 1 (team approval) for each client attachment approval
INSERT INTO approval_stages (approval_id, stage_order, required_permission, reviewed_by, status, comment, reviewed_at)
SELECT
    caa.id,
    1,
    'client.approve',
    caa.team_reviewed_by,
    caa.team_status,
    caa.team_rejection_comment,
    caa.team_reviewed_at
FROM client_attachment_approvals caa;

-- Stage 2 (approver approval) for each client attachment approval
INSERT INTO approval_stages (approval_id, stage_order, required_permission, reviewed_by, status, comment, reviewed_at)
SELECT
    caa.id,
    2,
    'client.approve',
    caa.approver_reviewed_by,
    caa.approver_status,
    caa.approver_rejection_comment,
    caa.approver_reviewed_at
FROM client_attachment_approvals caa;

-- 9. Seed default approval policies for each existing project
INSERT INTO approval_policies (project_id, entity_type, action, stages)
SELECT
    p.id,
    'route',
    NULL,
    '[{"order": 1, "required_permission": "route.approve", "team_scope": "any"}]'::jsonb
FROM projects p;

INSERT INTO approval_policies (project_id, entity_type, action, stages)
SELECT
    p.id,
    'client_attachment',
    NULL,
    '[{"order": 1, "required_permission": "client.approve", "team_scope": "other_team"}, {"order": 2, "required_permission": "client.approve", "team_scope": "any"}]'::jsonb
FROM projects p;

-- 10. Drop old tables
DROP TABLE IF EXISTS approval_requests CASCADE;
DROP TABLE IF EXISTS client_attachment_approvals CASCADE;
