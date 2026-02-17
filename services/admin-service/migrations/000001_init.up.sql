CREATE TABLE IF NOT EXISTS sites (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  site_id VARCHAR(64) NOT NULL,
  name VARCHAR(128) NOT NULL,
  domain VARCHAR(255) NOT NULL,
  widget_key VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_sites_site_id (site_id),
  UNIQUE KEY uq_sites_widget_key (widget_key),
  KEY idx_sites_domain (domain),
  KEY idx_sites_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
