CREATE TABLE system_settings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    base_url TEXT NOT NULL DEFAULT '',
    jwt_expiry TEXT NOT NULL DEFAULT '',
    refresh_token_expiry TEXT NOT NULL DEFAULT '',
    log_level TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Singleton row
INSERT INTO system_settings (id) VALUES ('00000000-0000-0000-0000-000000000003');
