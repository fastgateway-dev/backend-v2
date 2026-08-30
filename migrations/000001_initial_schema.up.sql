-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Users table
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(255) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL DEFAULT 'user',
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_role ON users(role);

-- API Tokens table
CREATE TABLE api_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    token_hash VARCHAR(255) NOT NULL UNIQUE,
    last_used_at TIMESTAMP WITH TIME ZONE,
    expires_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_api_tokens_user_id ON api_tokens(user_id);
CREATE INDEX idx_api_tokens_token_hash ON api_tokens(token_hash);

-- Projects table
CREATE TABLE projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    k8s_api_url VARCHAR(500) NOT NULL,
    k8s_token_encrypted TEXT NOT NULL,
    is_connected BOOLEAN NOT NULL DEFAULT false,
    last_connected_at TIMESTAMP WITH TIME ZONE,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_projects_name ON projects(name);
CREATE INDEX idx_projects_created_by ON projects(created_by);

-- Project Admins table
CREATE TABLE project_admins (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, user_id)
);

CREATE INDEX idx_project_admins_project_id ON project_admins(project_id);
CREATE INDEX idx_project_admins_user_id ON project_admins(user_id);

-- Teams table (global teams)
CREATE TABLE teams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL UNIQUE,
    description TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Project Team Roles table (team-project assignments with roles)
CREATE TABLE project_team_roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    role VARCHAR(50) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, team_id)
);

CREATE INDEX idx_project_team_roles_project_id ON project_team_roles(project_id);
CREATE INDEX idx_project_team_roles_team_id ON project_team_roles(team_id);
CREATE INDEX idx_project_team_roles_role ON project_team_roles(role);

-- Team Members table
CREATE TABLE team_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(team_id, user_id)
);

CREATE INDEX idx_team_members_team_id ON team_members(team_id);
CREATE INDEX idx_team_members_user_id ON team_members(user_id);

-- Domain Templates table
CREATE TABLE domain_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    exposure_type VARCHAR(50) NOT NULL DEFAULT 'public',
    annotations JSONB DEFAULT '{}',
    tls_policy VARCHAR(50) NOT NULL DEFAULT 'terminate',
    tls_mode VARCHAR(50) NOT NULL DEFAULT 'tls_only',
    http_port INTEGER NOT NULL DEFAULT 80,
    https_port INTEGER NOT NULL DEFAULT 443,
    external_traffic_policy VARCHAR(50),
    load_balancer_class VARCHAR(255),
    controller_name VARCHAR(500) NOT NULL DEFAULT 'gateway.envoyproxy.io/gatewayclass-controller',
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    k8s_gateway_class_name VARCHAR(255),
    k8s_envoy_proxy_name VARCHAR(255),
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, name)
);

CREATE INDEX idx_domain_templates_project_id ON domain_templates(project_id);
CREATE INDEX idx_domain_templates_status ON domain_templates(status);
CREATE INDEX idx_domain_templates_exposure_type ON domain_templates(exposure_type);
CREATE INDEX idx_domain_templates_tls_mode ON domain_templates(tls_mode);

-- Domains table
CREATE TABLE domains (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    domain_template_id UUID REFERENCES domain_templates(id),
    name VARCHAR(255) NOT NULL,
    hostname VARCHAR(255) NOT NULL,
    namespace VARCHAR(255) NOT NULL DEFAULT 'default',
    http_port INTEGER NOT NULL DEFAULT 80,
    https_port INTEGER NOT NULL DEFAULT 443,
    tls_mode VARCHAR(50) NOT NULL DEFAULT 'tls_only',
    tls_secret_name VARCHAR(255),
    tls_policy VARCHAR(50) NOT NULL DEFAULT 'terminate',
    k8s_gateway_name VARCHAR(255),
    k8s_gateway_class_name VARCHAR(255),
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, hostname)
);

CREATE INDEX idx_domains_project_id ON domains(project_id);
CREATE INDEX idx_domains_hostname ON domains(hostname);
CREATE INDEX idx_domains_status ON domains(status);
CREATE INDEX idx_domains_domain_template_id ON domains(domain_template_id);

-- Project Namespaces table for managing which namespaces a project can route to
CREATE TABLE project_namespaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    namespace VARCHAR(255) NOT NULL,
    reference_grant_created BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, namespace)
);

CREATE INDEX idx_project_namespaces_project_id ON project_namespaces(project_id);

-- Routes table
CREATE TABLE routes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    team_id UUID NOT NULL REFERENCES teams(id),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    protocol VARCHAR(20) NOT NULL DEFAULT 'http',
    config JSONB,
    status VARCHAR(50) NOT NULL DEFAULT 'pending_create',
    k8s_route_name VARCHAR(255),
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(domain_id, name)
);

CREATE INDEX idx_routes_domain_id ON routes(domain_id);
CREATE INDEX idx_routes_team_id ON routes(team_id);
CREATE INDEX idx_routes_status ON routes(status);

-- Security Policies table for Envoy Gateway SecurityPolicy CRD
CREATE TABLE security_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id UUID NOT NULL UNIQUE REFERENCES routes(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_security_policies_project_id ON security_policies(project_id);

-- Backend Traffic Policies table for Envoy Gateway BackendTrafficPolicy CRD
-- Supports per-route policies (current) and per-domain policies (future)
CREATE TABLE backend_traffic_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id UUID UNIQUE REFERENCES routes(id) ON DELETE CASCADE,
    domain_id UUID REFERENCES domains(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    -- Ensure either route_id OR domain_id is set, not both
    CONSTRAINT backend_traffic_policies_target_check CHECK (
        (route_id IS NOT NULL AND domain_id IS NULL) OR
        (route_id IS NULL AND domain_id IS NOT NULL)
    )
);

CREATE INDEX idx_backend_traffic_policies_project_id ON backend_traffic_policies(project_id);
CREATE INDEX idx_backend_traffic_policies_domain_id ON backend_traffic_policies(domain_id);

-- Approval Requests table
CREATE TABLE approval_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id UUID NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    action VARCHAR(50) NOT NULL,
    config_snapshot JSONB,
    previous_config JSONB,
    security_policy_snapshot JSONB,
    previous_security_policy JSONB,
    backend_traffic_policy_snapshot JSONB,
    previous_backend_traffic_policy JSONB,
    submitted_by UUID NOT NULL REFERENCES users(id),
    reviewed_by UUID REFERENCES users(id),
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    rejection_comment TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    reviewed_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_approval_requests_route_id ON approval_requests(route_id);
CREATE INDEX idx_approval_requests_status ON approval_requests(status);
CREATE INDEX idx_approval_requests_submitted_by ON approval_requests(submitted_by);

-- Audit Logs table
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    username VARCHAR(255) NOT NULL,
    action VARCHAR(100) NOT NULL,
    resource_type VARCHAR(100) NOT NULL,
    resource_id UUID,
    resource_name VARCHAR(255),
    details JSONB,
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_logs_project_id ON audit_logs(project_id);
CREATE INDEX idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX idx_audit_logs_resource_type ON audit_logs(resource_type);
CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at);

-- Default admin user is now seeded at application startup via environment variables.
-- See ADMIN_USERNAME, ADMIN_PASSWORD, ADMIN_EMAIL in the configuration.
