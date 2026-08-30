-- Add labels column to projects, domains, and routes
ALTER TABLE projects ADD COLUMN labels JSONB NOT NULL DEFAULT '{}';
ALTER TABLE domains ADD COLUMN labels JSONB NOT NULL DEFAULT '{}';
ALTER TABLE routes ADD COLUMN labels JSONB NOT NULL DEFAULT '{}';

-- GIN indexes for efficient JSONB containment queries
CREATE INDEX idx_projects_labels ON projects USING GIN (labels);
CREATE INDEX idx_domains_labels ON domains USING GIN (labels);
CREATE INDEX idx_routes_labels ON routes USING GIN (labels);
