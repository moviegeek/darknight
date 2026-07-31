-- 003_bilingual_titles.sql - bilingual titles + original title for display.
--
-- The library grid shows the original title as the primary title and a
-- conditional secondary title (Chinese for English-original films, English for
-- Chinese-original films, both otherwise). To support that we store the
-- original title plus the English and Chinese localized titles.
--
-- original_language and country already exist (migration 002); the enricher
-- previously never populated them, which this plan fixes. The index makes
-- future filtering / grouping by original language cheap.

ALTER TABLE movies ADD COLUMN original_title TEXT NOT NULL DEFAULT '';
ALTER TABLE movies ADD COLUMN title_en      TEXT NOT NULL DEFAULT '';
ALTER TABLE movies ADD COLUMN title_zh      TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_movies_original_language ON movies(original_language);
