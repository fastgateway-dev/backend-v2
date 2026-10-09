ALTER TABLE routes ADD COLUMN stream_id uuid REFERENCES streams(id) ON DELETE CASCADE;
ALTER TABLE routes ADD COLUMN listener_port integer;
ALTER TABLE routes ALTER COLUMN domain_id DROP NOT NULL;
ALTER TABLE routes ADD CONSTRAINT chk_route_single_owner
  CHECK ((domain_id IS NOT NULL) <> (stream_id IS NOT NULL));
CREATE UNIQUE INDEX idx_route_stream_proto_port
  ON routes (stream_id, protocol, listener_port) WHERE stream_id IS NOT NULL;
