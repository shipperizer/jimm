-- 1_16.sql is a migration to delete identitymodel defaults
DROP TABLE identity_model_defaults;

UPDATE versions SET major=1, minor=16 WHERE component='jimmdb';

