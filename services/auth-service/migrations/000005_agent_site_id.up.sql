ALTER TABLE agents
  ADD COLUMN site_id VARCHAR(64) NOT NULL DEFAULT '' AFTER display_name,
  ADD KEY idx_agents_site_id (site_id);
