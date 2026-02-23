CREATE TABLE IF NOT EXISTS super_admins (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  email VARCHAR(190) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  display_name VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL,
  token_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_super_admins_email (email),
  KEY idx_super_admins_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO super_admins (
  id,
  email,
  password_hash,
  display_name,
  status,
  token_version,
  created_at,
  updated_at
)
SELECT
  a.id,
  a.email,
  a.password_hash,
  a.display_name,
  a.status,
  IF(a.token_version = 0, 1, a.token_version),
  a.created_at,
  a.updated_at
FROM agents a
WHERE a.role = 'super_admin'
ON DUPLICATE KEY UPDATE
  password_hash = VALUES(password_hash),
  display_name = VALUES(display_name),
  status = VALUES(status),
  token_version = VALUES(token_version),
  updated_at = VALUES(updated_at);

DELETE FROM agents WHERE role = 'super_admin';
