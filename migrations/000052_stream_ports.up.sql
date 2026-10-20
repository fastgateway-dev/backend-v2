-- A stream claims the (transport, port) pairs its L4 routes may use; routes
-- then pick from those claims. Existing streams must keep working, so the
-- column is backfilled with every port they already use.
--
-- "Used" (spec: stream port claims, "Used means...") is a route row of the
-- stream that is not rejected, OR a pending create/update approval for a
-- route of the stream that proposes the port. Pending approvals are included
-- because approving one writes that port to the route; if the claim were
-- missing, the approved route would reference an unclaimed port.
ALTER TABLE streams ADD COLUMN ports JSONB NOT NULL DEFAULT '[]'::jsonb;

UPDATE streams s SET ports = COALESCE((
  SELECT jsonb_agg(jsonb_build_object('transport', t, 'port', p) ORDER BY t, p)
  FROM (
    SELECT upper(r.protocol) AS t, (r.config->>'listenerPort')::int AS p
      FROM routes r
     WHERE r.stream_id = s.id AND r.protocol IN ('tcp','udp') AND r.status <> 'rejected'
       AND r.config->>'listenerPort' IS NOT NULL
    UNION
    SELECT upper(r.protocol), (a.config_snapshot->'routeConfig'->>'listenerPort')::int
      FROM approvals a JOIN routes r ON r.id = a.entity_id
     WHERE r.stream_id = s.id AND r.protocol IN ('tcp','udp')
       AND a.entity_type = 'route' AND a.status = 'pending' AND a.action IN ('create','update')
       AND a.config_snapshot->'routeConfig'->>'listenerPort' IS NOT NULL
  ) used), '[]'::jsonb);
