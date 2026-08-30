-- Revert domain namespace default to 'default'
ALTER TABLE domains ALTER COLUMN namespace SET DEFAULT 'default';
