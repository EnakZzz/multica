ALTER TABLE agent
    ADD COLUMN IF NOT EXISTS display_name TEXT;

UPDATE agent
SET display_name = '规划 Agent'
WHERE builtin_key = 'multica/planner'
   OR (builtin_key IS NULL AND name = '规划Agent');

UPDATE agent
SET display_name = CASE builtin_key
    WHEN 'multica/plan-writer' THEN '计划撰写 Agent'
    WHEN 'multica/verifier' THEN '验证 Agent'
    WHEN 'multica/code-reviewer' THEN '代码评审 Agent'
    WHEN 'multica/debugging-agent' THEN '调试 Agent'
    ELSE display_name
END
WHERE builtin_key IN (
    'multica/plan-writer',
    'multica/verifier',
    'multica/code-reviewer',
    'multica/debugging-agent'
);
