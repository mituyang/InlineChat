ALTER TABLE messages
  ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'sent' AFTER client_msg_id,
  ADD KEY idx_messages_status (status);
