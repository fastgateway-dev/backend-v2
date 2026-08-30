-- Clients table (global, like teams)
CREATE TABLE clients (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL UNIQUE,
    description TEXT,
    contact_name VARCHAR(255),
    contact_email VARCHAR(255),
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_clients_team_id ON clients(team_id);
CREATE INDEX idx_clients_name ON clients(name);

-- Client IP addresses
CREATE TABLE client_ip_addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    cidr VARCHAR(50) NOT NULL,
    description VARCHAR(255),
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_client_ip_addresses_client_id ON client_ip_addresses(client_id);

-- Client-Route attachments (security config per attachment)
CREATE TABLE client_route_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    route_id UUID NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    enable_ip_allowlist BOOLEAN NOT NULL DEFAULT false,
    enable_basic_auth BOOLEAN NOT NULL DEFAULT false,
    enable_mtls BOOLEAN NOT NULL DEFAULT false,
    status VARCHAR(50) NOT NULL DEFAULT 'pending_attach',
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(client_id, route_id)
);

CREATE INDEX idx_client_route_attachments_client_id ON client_route_attachments(client_id);
CREATE INDEX idx_client_route_attachments_route_id ON client_route_attachments(route_id);
CREATE INDEX idx_client_route_attachments_status ON client_route_attachments(status);

-- Client attachment approval requests (dual-approval)
CREATE TABLE client_attachment_approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attachment_id UUID NOT NULL REFERENCES client_route_attachments(id) ON DELETE CASCADE,
    action VARCHAR(50) NOT NULL,
    submitted_by UUID NOT NULL REFERENCES users(id),
    -- Team approval (route team or client team, depending on who initiated)
    team_reviewed_by UUID REFERENCES users(id),
    team_status VARCHAR(50) NOT NULL DEFAULT 'pending',
    team_reviewed_at TIMESTAMP WITH TIME ZONE,
    team_rejection_comment TEXT,
    -- Approver team approval
    approver_reviewed_by UUID REFERENCES users(id),
    approver_status VARCHAR(50) NOT NULL DEFAULT 'pending',
    approver_reviewed_at TIMESTAMP WITH TIME ZONE,
    approver_rejection_comment TEXT,
    -- Overall status
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_client_attachment_approvals_attachment_id ON client_attachment_approvals(attachment_id);
CREATE INDEX idx_client_attachment_approvals_status ON client_attachment_approvals(status);
