-- Add approval settings to projects
ALTER TABLE projects ADD COLUMN approval_enabled BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE projects ADD COLUMN self_approval_allowed BOOLEAN NOT NULL DEFAULT false;

-- Add min_approvers to approval_stages
ALTER TABLE approval_stages ADD COLUMN min_approvers INTEGER NOT NULL DEFAULT 1;

-- Track multiple reviewers per stage
CREATE TABLE approval_stage_reviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stage_id UUID NOT NULL REFERENCES approval_stages(id) ON DELETE CASCADE,
    reviewer_id UUID NOT NULL REFERENCES users(id),
    decision VARCHAR(20) NOT NULL DEFAULT 'approved',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(stage_id, reviewer_id)
);

CREATE INDEX idx_approval_stage_reviews_stage_id ON approval_stage_reviews(stage_id);
