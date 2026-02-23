INSERT INTO agents (
  email,
  password_hash,
  display_name,
  role,
  status,
  token_version,
  created_at,
  updated_at
)
SELECT
  sa.email,
  sa.password_hash,
  CASE
    WHEN EXISTS (SELECT 1 FROM agents a WHERE a.display_name = sa.display_name) THEN CONCAT(
      LEFT(sa.display_name, GREATEST(1, 118 - CHAR_LENGTH(CAST(sa.id AS CHAR)))),
      '__sa_',
      CAST(sa.id AS CHAR)
    )
    ELSE sa.display_name
  END AS display_name,
  'super_admin' AS role,
  sa.status,
  IF(sa.token_version = 0, 1, sa.token_version),
  sa.created_at,
  sa.updated_at
FROM super_admins sa
WHERE NOT EXISTS (
  SELECT 1
  FROM agents a
  WHERE a.email = sa.email
);

DROP TABLE IF EXISTS super_admins;
