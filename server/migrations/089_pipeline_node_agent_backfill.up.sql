UPDATE pipeline_stage ps
SET agent_id = pr.agent_id
FROM pipeline_role pr
WHERE pr.pipeline_id = ps.pipeline_id
  AND pr.key = ps.role_key
  AND ps.agent_id IS NULL;
