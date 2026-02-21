SET @old_exists := (
  SELECT COUNT(*)
  FROM information_schema.tables
  WHERE table_schema = DATABASE() AND table_name = 'event_outbox'
);
SET @new_exists := (
  SELECT COUNT(*)
  FROM information_schema.tables
  WHERE table_schema = DATABASE() AND table_name = 'event_outboxes'
);
SET @rename_sql := IF(
  @new_exists = 1 AND @old_exists = 0,
  'RENAME TABLE event_outboxes TO event_outbox',
  'SELECT 1'
);
PREPARE stmt FROM @rename_sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
