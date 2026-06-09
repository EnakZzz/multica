DROP INDEX IF EXISTS idx_skill_proposal_workspace_validation;

ALTER TABLE skill_proposal
    DROP COLUMN IF EXISTS confidence,
    DROP COLUMN IF EXISTS gate_reason,
    DROP COLUMN IF EXISTS token_delta,
    DROP COLUMN IF EXISTS rejected_similar_count,
    DROP COLUMN IF EXISTS validation_score_after,
    DROP COLUMN IF EXISTS validation_score_before,
    DROP COLUMN IF EXISTS validation_status,
    DROP COLUMN IF EXISTS edit_ops;
