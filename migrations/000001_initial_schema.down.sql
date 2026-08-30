-- Drop tables in reverse order of creation (respecting foreign key constraints)
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS approval_requests;
DROP TABLE IF EXISTS backend_traffic_policies;
DROP TABLE IF EXISTS security_policies;
DROP TABLE IF EXISTS routes;
DROP TABLE IF EXISTS project_namespaces;
DROP TABLE IF EXISTS domains;
DROP TABLE IF EXISTS domain_templates;
DROP TABLE IF EXISTS team_members;
DROP TABLE IF EXISTS project_team_roles;
DROP TABLE IF EXISTS teams;
DROP TABLE IF EXISTS project_admins;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS users;

-- Drop extension
DROP EXTENSION IF EXISTS "pgcrypto";
