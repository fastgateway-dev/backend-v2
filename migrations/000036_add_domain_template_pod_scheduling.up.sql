ALTER TABLE domain_templates ADD COLUMN pod_placement JSONB;
ALTER TABLE domain_templates ADD COLUMN pdb_config JSONB;
ALTER TABLE domain_templates ADD COLUMN deployment_strategy JSONB;
