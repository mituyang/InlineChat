ALTER TABLE sites
  ADD COLUMN domain VARCHAR(255) NOT NULL DEFAULT '' AFTER name;

UPDATE sites s
JOIN (
  SELECT site_id, MIN(domain) AS domain
  FROM site_domains
  GROUP BY site_id
) d ON d.site_id = s.site_id
SET s.domain = d.domain;

ALTER TABLE sites
  ADD KEY idx_sites_domain (domain);

DROP TABLE IF EXISTS site_domains;
