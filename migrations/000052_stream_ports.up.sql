-- A stream claims the (transport, port) pairs its L4 routes may use; routes
-- then pick from those claims. Existing streams must keep working, so the
-- column is backfilled with every port they already use.
--
-- "Used" (spec: stream port claims, "Used means...") is any of:
--   1. a route row of the stream that is not rejected;
--   2. a pending create/update approval for a route of the stream that
--      proposes the port. Approving one writes that port to the route; if the
--      claim were missing, the approved route would reference an unclaimed
--      port;
--   3. the latest deployed version (highest route_versions.version) of a
--      non-rejected route of the stream. An approved update copies its port
--      into the row (status pending_deploy) while the cluster's route still
--      sits on the deployed port until the separate deploy step; without this
--      the startup reconcile would remove that live listener.
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
    UNION
    SELECT upper(r.protocol), (v.config_snapshot->'routeConfig'->>'listenerPort')::int
      FROM routes r
      JOIN LATERAL (SELECT rv.config_snapshot FROM route_versions rv
                     WHERE rv.route_id = r.id ORDER BY rv.version DESC LIMIT 1) v ON true
     WHERE r.stream_id = s.id AND r.protocol IN ('tcp','udp') AND r.status <> 'rejected'
       AND v.config_snapshot->'routeConfig'->>'listenerPort' IS NOT NULL
  ) used), '[]'::jsonb);
