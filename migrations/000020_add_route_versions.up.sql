CREATE TABLE route_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id UUID NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    version INT NOT NULL,
    config_snapshot JSONB NOT NULL,
    route_description TEXT,
    protocol VARCHAR(20) NOT NULL,
    security_mode VARCHAR(50) NOT NULL,
    change_description TEXT,
    approval_id UUID REFERENCES approvals(id),
    deployed_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(route_id, version)
);

CREATE INDEX idx_route_versions_route_id_created_at
    ON route_versions(route_id, created_at DESC);
