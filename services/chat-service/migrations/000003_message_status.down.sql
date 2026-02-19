ALTER TABLE messages
  DROP INDEX idx_messages_status,
  DROP COLUMN status;
