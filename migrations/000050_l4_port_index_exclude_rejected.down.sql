-- Restoring the stricter index fails if a rejected route shares a port with a
-- live one; surface that clearly instead of a bare duplicate-key error.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM routes
    WHERE stream_id IS NOT NULL AND listener_port IS NOT NULL
    GROUP BY stream_id, protocol, listener_port
    HAVING COUNT(*) > 1
  ) THEN
    RAISE EXCEPTION 'Cannot downgrade 000050: a rejected L4 route shares (stream, protocol, port) with another route (delete the rejected routes first)';
  END IF;
END $$;

DROP INDEX idx_route_stream_proto_port;
CREATE UNIQUE INDEX idx_route_stream_proto_port
  ON routes (stream_id, protocol, listener_port) WHERE stream_id IS NOT NULL;
