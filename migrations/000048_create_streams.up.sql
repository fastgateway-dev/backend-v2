CREATE TABLE streams (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name text NOT NULL,
  namespace text NOT NULL,
  gateway_template_id uuid NOT NULL REFERENCES domain_templates(id),
  k8s_gateway_name text NOT NULL,
  k8s_gateway_class text NOT NULL,
  status text NOT NULL DEFAULT 'pending',
  status_message text NOT NULL DEFAULT '',
  created_by uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_stream_project_name ON streams (project_id, name);
