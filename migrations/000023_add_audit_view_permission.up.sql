-- Add audit.view permission to existing builtin Admin presets
UPDATE permission_presets
SET permissions = array_append(permissions, 'audit.view'),
    updated_at = now()
WHERE is_builtin = true
  AND name = 'Admin'
  AND NOT ('audit.view' = ANY(permissions));
