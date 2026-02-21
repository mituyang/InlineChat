ALTER TABLE conversations
  ADD COLUMN pending_transfer_to_agent_id BIGINT UNSIGNED NULL AFTER assigned_agent_id,
  ADD COLUMN pending_transfer_from_agent_id BIGINT UNSIGNED NULL AFTER pending_transfer_to_agent_id,
  ADD COLUMN pending_transfer_requested_at DATETIME(3) NULL AFTER pending_transfer_from_agent_id,
  ADD KEY idx_conversations_pending_transfer_to_agent_id (pending_transfer_to_agent_id),
  ADD KEY idx_conversations_pending_transfer_from_agent_id (pending_transfer_from_agent_id),
  ADD KEY idx_conversations_pending_transfer_requested_at (pending_transfer_requested_at);
