-- GIN indexes accelerate JSONB containment queries used by the
-- project-scoped route listing endpoint when filtering by backend
-- service+namespace.
CREATE INDEX IF NOT EXISTS idx_routes_config_backends
    ON routes USING GIN ((config -> 'backends') jsonb_path_ops);

CREATE INDEX IF NOT EXISTS idx_routes_config_mirrors
    ON routes USING GIN ((config -> 'mirrors') jsonb_path_ops);
