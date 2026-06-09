ALTER TABLE skill_proposal
    ADD COLUMN edit_ops JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN validation_status TEXT NOT NULL DEFAULT 'not_run'
        CHECK (validation_status IN ('not_run', 'skipped', 'passed', 'failed')),
    ADD COLUMN validation_score_before DOUBLE PRECISION,
    ADD COLUMN validation_score_after DOUBLE PRECISION,
    ADD COLUMN rejected_similar_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN token_delta INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN gate_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN confidence TEXT NOT NULL DEFAULT 'unknown'
        CHECK (confidence IN ('unknown', 'low', 'medium', 'high'));

CREATE INDEX idx_skill_proposal_workspace_validation
    ON skill_proposal(workspace_id, validation_status, created_at DESC);
