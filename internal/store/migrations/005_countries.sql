-- 005_countries.sql - store every TMDB production country, not just the first.
--
-- `country` (migration 002) already holds the primary ISO code used for
-- filtering and stays as-is. `countries` adds the full list as a
-- comma-separated string of ISO 3166-1 codes (e.g. "JP,US") so the UI can
-- show every country a film is credited to, not just the first.

ALTER TABLE movies ADD COLUMN countries TEXT NOT NULL DEFAULT '';
