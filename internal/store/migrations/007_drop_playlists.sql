-- 007_drop_playlists.sql - remove the user-curated playlists feature.
--
-- The "播放列表" (playlists) nav item and its placeholder page have been
-- replaced by the system "合集" (collections) view, which lists the TMDB
-- collections already discovered during scan. The playlists tables and their
-- model type are dropped entirely; user_collections / user_collection_items
-- (a separate, unrelated feature) are left untouched.

DROP TABLE IF EXISTS playlist_items;
DROP TABLE IF EXISTS playlists;
