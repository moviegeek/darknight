-- 002_subtitle_country.sql — subtitle aggregation + country/original language.

-- Aggregate subtitle info on movie_files so the library grid can filter and
-- badge without joining subtitles for every row.
ALTER TABLE movie_files ADD COLUMN subtitle_languages TEXT NOT NULL DEFAULT '';
ALTER TABLE movie_files ADD COLUMN has_external_subtitle INTEGER NOT NULL DEFAULT 0;

-- Country / original language for the movie-level country filter.
ALTER TABLE movies ADD COLUMN country TEXT NOT NULL DEFAULT '';
ALTER TABLE movies ADD COLUMN original_language TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_movies_country ON movies(country);

-- Helpful index for subtitle lookups during filtering.
CREATE INDEX idx_subtitles_language ON subtitles(language);
