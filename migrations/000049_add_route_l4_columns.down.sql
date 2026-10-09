DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM routes WHERE stream_id IS NOT NULL) THEN
    RAISE EXCEPTION 'Cannot downgrade 000049: stream-owned routes still exist (remove them first)';
  END IF;
END $$;

DROP INDEX idx_route_stream_proto_port;
ALTER TABLE routes DROP CONSTRAINT chk_route_single_owner;
ALTER TABLE routes
  DROP COLUMN listener_port,
  DROP COLUMN stream_id;
ALTER TABLE routes ALTER COLUMN domain_id SET NOT NULL;
