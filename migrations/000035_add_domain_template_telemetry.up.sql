ALTER TABLE domain_templates ADD COLUMN telemetry_access_log JSONB;
ALTER TABLE domain_templates ADD COLUMN telemetry_tracing JSONB;
ALTER TABLE domain_templates ADD COLUMN telemetry_metrics JSONB;
