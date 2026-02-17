ALTER TABLE conversations
  ADD COLUMN assigned_agent_id BIGINT UNSIGNED NULL AFTER status,
  ADD COLUMN closed_at DATETIME(3) NULL AFTER updated_at,
  ADD COLUMN closed_by_agent_id BIGINT UNSIGNED NULL AFTER closed_at,
  ADD KEY idx_conversations_assigned_agent_id (assigned_agent_id),
  ADD KEY idx_conversations_closed_at (closed_at),
  ADD KEY idx_conversations_closed_by_agent_id (closed_by_agent_id);
