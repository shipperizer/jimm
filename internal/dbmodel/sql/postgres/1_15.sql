-- 1_15.sql is a migration to delete controller configs
DROP TABLE controller_configs;

UPDATE versions SET major=1, minor=15 WHERE component='jimmdb';

