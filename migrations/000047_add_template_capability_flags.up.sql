ALTER TABLE domain_templates
  ADD COLUMN enable_domain BOOLEAN NOT NULL DEFAULT true,
  ADD COLUMN enable_stream BOOLEAN NOT NULL DEFAULT false;
