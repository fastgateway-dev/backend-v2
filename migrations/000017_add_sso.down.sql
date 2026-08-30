DROP TABLE IF EXISTS team_email_invites;
DROP TABLE IF EXISTS sso_config;

ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
ALTER TABLE users DROP COLUMN IF EXISTS provider_subject;
ALTER TABLE users DROP COLUMN IF EXISTS auth_provider;

DROP INDEX IF EXISTS idx_users_provider_subject;
