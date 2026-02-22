ALTER TABLE conversations
  ADD KEY idx_conversations_site_visitor_status_id (site_id, visitor_token, status, id),
  ADD KEY idx_conversations_status_assigned_id (status, assigned_agent_id, id);

ALTER TABLE messages
  ADD KEY idx_messages_conversation_sender_status_id (conversation_id, sender_type, status, id),
  ADD KEY idx_messages_conversation_id_id (conversation_id, id);

ALTER TABLE event_outboxes
  ADD KEY idx_event_outboxes_status_retry_id (status, next_retry_at, id),
  ADD KEY idx_event_outboxes_status_processing (status, processing_at);
