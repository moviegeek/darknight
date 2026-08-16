-- 010: normalized title key for seed re-attachment
--
-- The scanner reattaches a release to its movie row by comparing the parsed
-- title against the stored title variants with exact SQL equality. Release
-- names and TMDB titles routinely differ in case, punctuation and diacritics
-- ("As Good As It Gets" vs "As Good as It Gets", "Czlowiek" vs "Człowiek"),
-- so every such release created a DUPLICATE movie row while the enriched row
-- was left with no files (observed: 169 duplicates against 148 orphans).
--
-- match_key holds store.MatchKey(title) - lowercased, diacritic-folded, with
-- punctuation removed. Go backfills it on migration (see store.Open) because
-- SQLite cannot do the Unicode folding; the column starts empty and the
-- backfill fills it for existing rows.

ALTER TABLE movies ADD COLUMN match_key TEXT NOT NULL DEFAULT '';

-- Non-unique: two genuinely different films can share a folded key when their
-- years differ (remakes), so the seed lookup pairs match_key WITH year.
CREATE INDEX idx_movies_match_key ON movies(match_key);
