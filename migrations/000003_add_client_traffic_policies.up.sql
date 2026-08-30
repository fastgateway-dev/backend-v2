-- Create client_traffic_policies table for domain-level client traffic configuration
CREATE TABLE IF NOT EXISTS client_traffic_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID NOT NULL UNIQUE REFERENCES domains(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    config JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create index for project_id for efficient queries
CREATE INDEX IF NOT EXISTS idx_client_traffic_policies_project_id ON client_traffic_policies(project_id);
