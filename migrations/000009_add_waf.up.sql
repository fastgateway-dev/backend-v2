-- Create waf_policies table for WAF (Web Application Firewall) using coraza-proxy-wasm
CREATE TABLE IF NOT EXISTS waf_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id UUID NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(route_id)
);

CREATE INDEX idx_waf_policies_route_id ON waf_policies(route_id);
CREATE INDEX idx_waf_policies_project_id ON waf_policies(project_id);

-- Add WAF policy snapshot columns to approval_requests table
ALTER TABLE approval_requests
ADD COLUMN IF NOT EXISTS waf_policy_snapshot JSONB,
ADD COLUMN IF NOT EXISTS previous_waf_policy JSONB;
