ALTER TABLE agent
    ADD COLUMN is_internal BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN agent.is_internal IS
    'System-managed agent seeded by Multica; users cannot update or archive it directly.';
