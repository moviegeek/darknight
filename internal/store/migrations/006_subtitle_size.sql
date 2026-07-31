-- 006_subtitle_size.sql - track the file size of external subtitle files.
--
-- The version comparison table wants to show external subtitles with their
-- size (like it already does for the video file); embedded subtitles have no
-- size of their own so this stays 0 for them.

ALTER TABLE subtitles ADD COLUMN file_size INTEGER NOT NULL DEFAULT 0;
