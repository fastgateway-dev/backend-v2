-- Create envoy_extension_policies table for Lua and Wasm extensions
CREATE TABLE IF NOT EXISTS envoy_extension_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id UUID NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(route_id)
);

CREATE INDEX idx_envoy_extension_policies_route_id ON envoy_extension_policies(route_id);
CREATE INDEX idx_envoy_extension_policies_project_id ON envoy_extension_policies(project_id);

-- Add extension policy snapshot columns to approval_requests table
ALTER TABLE approval_requests
ADD COLUMN IF NOT EXISTS envoy_extension_policy_snapshot JSONB,
ADD COLUMN IF NOT EXISTS previous_envoy_extension_policy JSONB;
