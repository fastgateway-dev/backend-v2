-- Re-add old columns with their original defaults.
ALTER TABLE domain_templates
  ADD COLUMN tls_mode VARCHAR(50) NOT NULL DEFAULT 'tls_only',
  ADD COLUMN tls_policy VARCHAR(50) NOT NULL DEFAULT 'terminate',
  ADD COLUMN http_port INTEGER NOT NULL DEFAULT 80,
  ADD COLUMN https_port INTEGER NOT NULL DEFAULT 443,
  ADD COLUMN enable_domain BOOLEAN NOT NULL DEFAULT true,
  ADD COLUMN enable_stream BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE domains
  ADD COLUMN tls_mode VARCHAR(50) NOT NULL DEFAULT 'tls_only',
  ADD COLUMN tls_policy VARCHAR(50) NOT NULL DEFAULT 'terminate',
  ADD COLUMN http_port INTEGER NOT NULL DEFAULT 80,
  ADD COLUMN https_port INTEGER NOT NULL DEFAULT 443;

-- Best-effort reconstruction from listeners.
UPDATE domain_templates SET
  enable_stream = (listeners @> '[{"protocol":"TCP"}]' OR listeners @> '[{"protocol":"UDP"}]'),
  enable_domain = (listeners @> '[{"protocol":"HTTP"}]' OR listeners @> '[{"protocol":"HTTPS"}]'),
  http_port  = COALESCE((SELECT (e->>'port')::int FROM jsonb_array_elements(listeners) e WHERE e->>'protocol'='HTTP'  LIMIT 1), 80),
  https_port = COALESCE((SELECT (e->>'port')::int FROM jsonb_array_elements(listeners) e WHERE e->>'protocol'='HTTPS' LIMIT 1), 443),
  tls_mode = CASE
    WHEN listeners @> '[{"protocol":"HTTP"}]' AND listeners @> '[{"protocol":"HTTPS"}]' THEN 'both'
    WHEN listeners @> '[{"protocol":"HTTP"}]' THEN 'no_tls'
    ELSE 'tls_only' END,
  tls_policy = COALESCE((SELECT lower(e->>'tlsMode') FROM jsonb_array_elements(listeners) e WHERE e->>'protocol'='HTTPS' LIMIT 1), 'terminate');

ALTER TABLE domain_templates DROP COLUMN listeners;
ALTER TABLE domains DROP COLUMN bound_listeners;
CREATE INDEX idx_domain_templates_tls_mode ON domain_templates(tls_mode);
