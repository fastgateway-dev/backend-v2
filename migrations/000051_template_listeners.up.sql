-- Add new columns (nullable JSONB, matches 000035 telemetry pattern)
ALTER TABLE domain_templates ADD COLUMN listeners JSONB;
ALTER TABLE domains ADD COLUMN bound_listeners JSONB;

-- Backfill domain_templates.listeners from tls_mode/http_port/https_port/tls_policy/enable_stream.
-- Listener names "http"/"https" are preserved verbatim (byte-identical invariant).
UPDATE domain_templates SET listeners = (
  (CASE
     WHEN tls_mode = 'no_tls'  THEN jsonb_build_array(
       jsonb_build_object('name','http','protocol','HTTP','port',http_port))
     WHEN tls_mode = 'both'    THEN jsonb_build_array(
       jsonb_build_object('name','http','protocol','HTTP','port',http_port),
       jsonb_build_object('name','https','protocol','HTTPS','port',https_port,
         'tlsMode', CASE WHEN tls_policy='passthrough' THEN 'Passthrough' ELSE 'Terminate' END))
     ELSE jsonb_build_array(  -- tls_only + unknown
       jsonb_build_object('name','https','protocol','HTTPS','port',https_port,
         'tlsMode', CASE WHEN tls_policy='passthrough' THEN 'Passthrough' ELSE 'Terminate' END))
   END)
  ||
  (CASE WHEN enable_stream THEN jsonb_build_array(
       jsonb_build_object('name','tcpudp','protocol','TCP','portRangeMin',1,'portRangeMax',65535))
     ELSE '[]'::jsonb END)
);

-- Backfill domains.bound_listeners from their tls_mode.
UPDATE domains SET bound_listeners = (CASE
  WHEN tls_mode = 'no_tls' THEN jsonb_build_array('http')
  WHEN tls_mode = 'both'   THEN jsonb_build_array('http','https')
  ELSE jsonb_build_array('https')
END);

-- Make new columns NOT NULL now that they're populated.
ALTER TABLE domain_templates ALTER COLUMN listeners SET NOT NULL;
ALTER TABLE domains ALTER COLUMN bound_listeners SET NOT NULL;

-- Drop the index that references tls_mode, then the old columns (hard cutover).
DROP INDEX IF EXISTS idx_domain_templates_tls_mode;
ALTER TABLE domain_templates
  DROP COLUMN tls_mode, DROP COLUMN tls_policy,
  DROP COLUMN http_port, DROP COLUMN https_port,
  DROP COLUMN enable_domain, DROP COLUMN enable_stream;
ALTER TABLE domains
  DROP COLUMN tls_mode, DROP COLUMN tls_policy,
  DROP COLUMN http_port, DROP COLUMN https_port;
