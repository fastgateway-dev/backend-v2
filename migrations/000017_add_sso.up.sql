-- Add SSO columns to users
ALTER TABLE users ADD COLUMN auth_provider VARCHAR(50) NOT NULL DEFAULT 'local';
ALTER TABLE users ADD COLUMN provider_subject VARCHAR(255);
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

CREATE INDEX idx_users_provider_subject ON users(provider_subject);

-- SSO configuration (singleton)
CREATE TABLE sso_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enabled BOOLEAN NOT NULL DEFAULT false,
    provider_name VARCHAR(255) NOT NULL DEFAULT '',
    issuer_url VARCHAR(500) NOT NULL DEFAULT '',
    client_id VARCHAR(255) NOT NULL DEFAULT '',
    client_secret_encrypted TEXT NOT NULL DEFAULT '',
    scopes TEXT[] NOT NULL DEFAULT '{openid,email,profile}',
    allowed_domains TEXT[],
    auto_register BOOLEAN NOT NULL DEFAULT true,
    force_sso BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Insert singleton row
INSERT INTO sso_config (id, enabled) VALUES ('00000000-0000-0000-0000-000000000002', false);

-- Team email invites (pre-add users by email)
CREATE TABLE team_email_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    invited_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(team_id, email)
);

CREATE INDEX idx_team_email_invites_email ON team_email_invites(email);
CREATE INDEX idx_team_email_invites_team_id ON team_email_invites(team_id);
