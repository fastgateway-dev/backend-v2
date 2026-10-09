-- Rejected routes never reach the cluster and do not hold their listener port
-- (the service-level collision check excludes them); make the unique index
-- agree so a port can be re-used after a rejection.
DROP INDEX idx_route_stream_proto_port;
CREATE UNIQUE INDEX idx_route_stream_proto_port
  ON routes (stream_id, protocol, listener_port)
  WHERE stream_id IS NOT NULL AND status <> 'rejected';
