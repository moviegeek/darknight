// Types mirroring the Go backend's JSON. Kept hand-written (no codegen) for
// simplicity — the surface is small and stable.

export interface Library {
  id: number;
  name: string;
  root_path: string;
  scan_interval: number;
  last_scan_at: number;
  created_at: number;
  updated_at: number;
}

export interface Movie {
  id: number;
  title: string;
  sort_title: string;
  year: number;
  release_date: string;
  runtime: number;
  synopsis: string;
  poster_path: string;
  backdrop_path: string;
  tmdb_id: number;
  imdb_id: string;
  vote_average: number;
  vote_count: number;
  collection_id: number;
  country: string;
  countries: string;
  original_language: string;
  original_title: string;
  title_en: string;
  title_zh: string;
  // Match state machine + audit fields, emitted by the backend on every movie
  // response. The console's data-health panel relies on them.
  match_status: string; // matched | pending | unmatched | manual
  fail_reason: string;
  created_at: number;
  updated_at: number;
}

// Genre attached to the movie detail response.
export interface Genre {
  id: number;
  name: string;
}

// A system movie collection (series / anthology), sourced from TMDB during
// scan. Listed under the "合集" tab.
export interface Collection {
  id: number;
  name: string;
  tmdb_id: number;
  poster_path: string;
  backdrop_path: string;
  overview: string;
  created_at: number;
  updated_at: number;
}

// A collection plus how many of its member movies are present in the library,
// and the total number of films in the TMDB collection. The grid badge renders
// "已有/总数 部"; total_parts is 0 when the collection was never refreshed
// against TMDB, in which case the UI falls back to "已有/已有".
export interface CollectionWithCount extends Collection {
  movie_count: number;
  total_parts: number;
}

// One member movie of a collection, cached from TMDB's /collection/{id} parts
// array (release order). local_movie_id is the row id of the matching local
// movie (joined on tmdb_id) when the film is in the library, or null when it
// is missing. The detail page interleaves owned and missing films in order.
export interface CollectionPart {
  id: number;
  collection_id: number;
  tmdb_id: number;
  title: string;
  original_title: string;
  release_date: string;
  poster_path: string;
  overview: string;
  vote_average: number;
  order: number;
  local_movie_id: number | null;
}

// MovieDetail is the response shape of GET /api/movies/:id - a Movie plus the
// genres + collection the detail page renders inline.
export interface MovieDetail extends Movie {
  genres: Genre[];
}

// Cast / crew returned by GET /api/movies/:id/cast.
export interface Person {
  id: number;
  name: string;
  tmdb_id: number;
  profile_path: string;
}
export interface Credit {
  movie_id: number;
  person_id: number;
  role: "cast" | "crew";
  job: string;
  character: string;
  order: number;
}
export interface CastResponse {
  cast: Credit[];
  crew: Credit[];
  people: Record<number, Person>;
}

export interface MovieListItem extends Movie {
  has_files: boolean;
  file_count: number;
  best_resolution: string;
  best_source: string;
  best_hdr: string;
  dolby_vision: boolean;
  watched: boolean;
  has_chi_subtitle: boolean;
  has_external_subtitle: boolean;
}

export interface MovieFile {
  id: number;
  movie_id: number;
  library_id: number;
  library_name: string;
  dir_path: string;
  file_name: string;
  is_disc: boolean;
  file_size: number;
  file_modified: number;
  release_group: string;
  edition: string;
  source: string;
  resolution: string;
  video_codec: string;
  audio_codec: string;
  audio_channels: string;
  hdr: string;
  dolby_vision: boolean;
  bit_depth: number;
  audio_count: number;
  language: string;
  is_collection: boolean;
  raw_name: string;
  duration_sec: number;
  video_bitrate: number;
  frame_rate: number;
  width: number;
  height: number;
  container: string;
  nfo_path: string;
  scanned_at: number;
}

export interface AudioTrack {
  id: number;
  movie_file_id: number;
  language: string;
  codec: string;
  channels: number;
  title: string;
  is_default: boolean;
  is_lossless: boolean;
  order: number;
}

export interface Subtitle {
  id: number;
  movie_file_id: number;
  file_path: string;
  language: string;
  format: string;
  is_embedded: boolean;
  is_default: boolean;
  order: number;
  file_size: number;
}

export interface MovieFileDetail extends MovieFile {
  audio_tracks: AudioTrack[];
  subtitles: Subtitle[];
}

export interface MovieListResponse {
  items: MovieListItem[];
  total: number;
  limit: number;
  offset: number;
}

// One row of GET /countries: an ISO production-country code and how many
// movies in the whole library carry it, unfiltered.
export interface CountryCount {
  code: string;
  count: number;
}

// Result of GET /movies/facets: for the currently active filters, how many
// movies would match if each chip were (additionally) selected.
export interface MovieFacets {
  resolution: Record<string, number>;
  source: Record<string, number>;
  video_codec: Record<string, number>;
  hdr: Record<string, number>;
  dolby_vision: number;
  country: Record<string, number>;
  subtitle_lang: Record<string, number>;
  external_subtitle: number;
  no_chi_subtitle: number;
  watched: Record<string, number>;
  // Data-health buckets. "unmatched" is the union of no_files + no_tmdb, so
  // its count is not the sum of the two.
  match_issue: Record<string, number>;
  // Match state machine counts (matched/pending/unmatched/manual).
  match_status: Record<string, number>;
}

// Query params for the movies list. Empty values are omitted.
export interface MovieQuery {
  q?: string;
  genre?: string;
  year_from?: number;
  year_to?: number;
  resolution?: string;
  source?: string;
  codec?: string;
  hdr?: string;
  dolby_vision?: boolean;
  collection?: number;
  watched?: string;
  country?: string;
  subtitle_lang?: string;
  external_subtitle?: boolean;
  no_chi_subtitle?: boolean;
  // Data-health filter: "unmatched" | "no_files" | "no_tmdb" | "multi_version".
  match_issue?: string;
  // Match state machine: "matched" | "pending" | "unmatched" | "manual".
  match_status?: string;
  sort?: string;
  desc?: boolean;
  limit?: number;
  offset?: number;
}

// ----- SQL console -----

export interface TableColumn {
  cid: number;
  name: string;
  type: string;
  notnull: number;
  pk: number;
}

export interface TableInfo {
  name: string;
  columns: TableColumn[];
}

export interface SqlResult {
  columns: string[];
  rows: unknown[][];
  rows_affected: number;
  last_insert_id?: number;
  duration_ms: number;
}

// ----- manual matching & rename -----

// One scored TMDB search result, as returned by the candidates endpoint and
// stored on pending movie rows.
export interface MatchCandidate {
  tmdb_id: number;
  title: string;
  original_title: string;
  year: number;
  poster_path: string;
  overview: string;
  score: number;
  year_diff: number;
}

export interface CandidatesResponse {
  candidates: MatchCandidate[];
}

// One filesystem rename computed server-side: from -> to, both absolute
// server paths (display only - the client never sends paths back).
export interface RenameMove {
  from: string;
  to: string;
  kind: "video" | "subtitle" | "nfo" | "other";
}

export interface RenamePlan {
  dir_old: string;
  dir_new: string;
  moves: RenameMove[];
}

// Per-release rename preview attached to a successful manual match.
export interface RenamePreview {
  movie_file_id: number;
  plan: RenamePlan;
}

export interface MatchResponse {
  movie_id: number;
  tmdb_id: number;
  status: string;
  merged_into?: number;
  files_moved?: number;
  rename_preview?: RenamePreview[];
}

export interface RenameResponse {
  changed: boolean;
  dry_run?: boolean;
  plan: RenamePlan;
  dir_old?: string;
  dir_new?: string;
}

// ----- subtitle upload -----

// One uploaded subtitle as confirmed by the user: the file (already staged by
// the browser) and the language they picked for it.
export interface SubtitleUploadItem {
  filename: string;
  lang: string; // display name; the backend resolves ISO 639-2
  size: number;
}

export interface SubtitleUploadResult {
  original_name: string;
  final_name: string;
  lang: string;
  size: number;
}
