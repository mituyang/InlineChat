ALTER TABLE agents
  DROP KEY idx_agents_site_id,
  DROP COLUMN site_id;
