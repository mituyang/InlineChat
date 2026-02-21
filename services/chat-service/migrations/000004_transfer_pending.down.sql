ALTER TABLE conversations
  DROP INDEX idx_conversations_pending_transfer_requested_at,
  DROP INDEX idx_conversations_pending_transfer_from_agent_id,
  DROP INDEX idx_conversations_pending_transfer_to_agent_id,
  DROP COLUMN pending_transfer_requested_at,
  DROP COLUMN pending_transfer_from_agent_id,
  DROP COLUMN pending_transfer_to_agent_id;
