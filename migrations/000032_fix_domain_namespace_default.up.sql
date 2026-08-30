-- Fix domain namespace default from 'default' to 'fastgateway-system'
-- and update any stale rows that may have the old default
ALTER TABLE domains ALTER COLUMN namespace SET DEFAULT 'fastgateway-system';
UPDATE domains SET namespace = 'fastgateway-system' WHERE namespace = 'default';
