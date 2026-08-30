ALTER TABLE project_namespaces
    ADD COLUMN capabilities TEXT[] NOT NULL DEFAULT '{}';

UPDATE project_namespaces
SET capabilities = ARRAY['backend_service','tls_secret']::TEXT[]
WHERE cardinality(capabilities) = 0;

CREATE INDEX idx_project_namespaces_capabilities
    ON project_namespaces USING GIN (capabilities);
