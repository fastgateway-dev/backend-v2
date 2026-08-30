ALTER TABLE domain_templates
    DROP COLUMN IF EXISTS pod_annotations,
    DROP COLUMN IF EXISTS container_resources,
    DROP COLUMN IF EXISTS scaling_config;
