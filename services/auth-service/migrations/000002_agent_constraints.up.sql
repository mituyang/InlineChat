UPDATE agents AS a
JOIN agents AS a2
  ON a.display_name = a2.display_name
  AND a.id > a2.id
SET a.display_name = CONCAT(
  LEFT(a.display_name, GREATEST(1, 121 - CHAR_LENGTH(CAST(a.id AS CHAR)))),
  '__dup_',
  CAST(a.id AS CHAR)
);

ALTER TABLE agents
  ADD UNIQUE KEY uq_agents_display_name (display_name);
