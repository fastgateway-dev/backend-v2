-- Reverse: recreate old tables, migrate data back, drop new tables
-- This is destructive and loses new-format data

CREATE TABLE IF NOT EXISTS approval_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id UUID NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    action VARCHAR(50) NOT NULL,
    config_snapshot JSONB,
    previous_config JSONB,
    security_policy_snapshot JSONB,
    previous_security_policy JSONB,
    backend_traffic_policy_snapshot JSONB,
    previous_backend_traffic_policy JSONB,
    envoy_extension_policy_snapshot JSONB,
    previous_envoy_extension_policy JSONB,
    waf_policy_snapshot JSONB,
    previous_waf_policy JSONB,
    submitted_by UUID NOT NULL REFERENCES users(id),
    reviewed_by UUID REFERENCES users(id),
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    rejection_comment TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    reviewed_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS client_attachment_approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attachment_id UUID NOT NULL REFERENCES client_route_attachments(id) ON DELETE CASCADE,
    action VARCHAR(50) NOT NULL,
    submitted_by UUID NOT NULL REFERENCES users(id),
    team_reviewed_by UUID REFERENCES users(id),
    team_status VARCHAR(50) NOT NULL DEFAULT 'pending',
    team_reviewed_at TIMESTAMP WITH TIME ZONE,
    team_rejection_comment TEXT,
    approver_reviewed_by UUID REFERENCES users(id),
    approver_status VARCHAR(50) NOT NULL DEFAULT 'pending',
    approver_reviewed_at TIMESTAMP WITH TIME ZONE,
    approver_rejection_comment TEXT,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

ALTER TABLE project_team_roles ADD COLUMN role VARCHAR(50) NOT NULL DEFAULT 'viewer';

-- Best-effort migration: infer role from permissions
UPDATE project_team_roles SET role = 'approver'
WHERE 'route.approve' = ANY(permissions);

UPDATE project_team_roles SET role = 'editor'
WHERE 'route.create' = ANY(permissions) AND role = 'viewer';

ALTER TABLE project_team_roles DROP COLUMN permissions;

DROP TABLE IF EXISTS approval_policies CASCADE;
DROP TABLE IF EXISTS approval_stages CASCADE;
DROP TABLE IF EXISTS approvals CASCADE;
