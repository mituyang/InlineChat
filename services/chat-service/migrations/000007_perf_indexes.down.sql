ALTER TABLE event_outboxes
  DROP INDEX idx_event_outboxes_status_processing,
  DROP INDEX idx_event_outboxes_status_retry_id;

ALTER TABLE messages
  DROP INDEX idx_messages_conversation_id_id,
  DROP INDEX idx_messages_conversation_sender_status_id;

ALTER TABLE conversations
  DROP INDEX idx_conversations_status_assigned_id,
  DROP INDEX idx_conversations_site_visitor_status_id;
