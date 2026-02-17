ALTER TABLE conversations
  DROP INDEX idx_conversations_closed_by_agent_id,
  DROP INDEX idx_conversations_closed_at,
  DROP INDEX idx_conversations_assigned_agent_id,
  DROP COLUMN closed_by_agent_id,
  DROP COLUMN closed_at,
  DROP COLUMN assigned_agent_id;
