-- Remove audit.view permission from builtin Admin presets
UPDATE permission_presets
SET permissions = array_remove(permissions, 'audit.view'),
    updated_at = now()
WHERE is_builtin = true
  AND name = 'Admin'
  AND 'audit.view' = ANY(permissions);
