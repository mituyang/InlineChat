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
  @old_exists = 1 AND @new_exists = 0,
  'RENAME TABLE event_outbox TO event_outboxes',
  'SELECT 1'
);
PREPARE stmt FROM @rename_sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
