ALTER TABLE domain_templates
    ADD COLUMN pod_annotations JSONB DEFAULT '{}',
    ADD COLUMN container_resources JSONB,
    ADD COLUMN scaling_config JSONB;
