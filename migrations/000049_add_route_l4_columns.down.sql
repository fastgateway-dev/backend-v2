DROP INDEX idx_route_stream_proto_port;
ALTER TABLE routes DROP CONSTRAINT chk_route_single_owner;
ALTER TABLE routes
  DROP COLUMN listener_port,
  DROP COLUMN stream_id;
ALTER TABLE routes ALTER COLUMN domain_id SET NOT NULL;
