CREATE TABLE IF NOT EXISTS site_domains (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  site_id VARCHAR(64) NOT NULL,
  domain VARCHAR(255) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_site_domains_domain (domain),
  UNIQUE KEY uq_site_domains_site_id_domain (site_id, domain),
  KEY idx_site_domains_site_id (site_id),
  CONSTRAINT fk_site_domains_site_id FOREIGN KEY (site_id) REFERENCES sites(site_id) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO site_domains (site_id, domain, created_at, updated_at)
SELECT site_id, LOWER(TRIM(domain)), created_at, updated_at
FROM sites
WHERE TRIM(domain) <> ''
ON DUPLICATE KEY UPDATE
  site_id = VALUES(site_id),
  updated_at = VALUES(updated_at);

ALTER TABLE sites
  DROP INDEX idx_sites_domain,
  DROP COLUMN domain;
