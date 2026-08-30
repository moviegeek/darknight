// Thin fetch wrapper around the REST API. All paths are relative — in dev the
// Vite proxy forwards /api to :8080, in prod the Go binary serves both.
import type {
  CastResponse,
  Collection,
  CollectionPart,
  CollectionWithCount,
  CountryCount,
  Genre,
  Library,
  MovieDetail,
  MovieFacets,
  MovieFile,
  MovieFileDetail,
  MatchResponse,
  MovieListResponse,
  SubtitleUploadResult,
  MovieQuery,
  RenameResponse,
  SqlResult,
  TableInfo,
  CandidatesResponse,
} from "./types";

export async function http<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { Accept: "application/json", ...(init?.headers ?? {}) },
    ...init,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`${res.status} ${res.statusText} ${text}`);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

function qs(params: MovieQuery): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    if (typeof v === "boolean") {
      if (v) sp.set(k, "1");
    } else {
      sp.set(k, String(v));
    }
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

// ----- libraries -----
export const listLibraries = () => http<Library[]>("/api/libraries");
export const createLibrary = (name: string, rootPath: string, scanInterval = 0) =>
  http<Library>("/api/libraries", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, root_path: rootPath, scan_interval: scanInterval }),
  });
export const deleteLibrary = (id: number) =>
  http<void>(`/api/libraries/${id}`, { method: "DELETE" });
export const scanLibrary = (id: number) =>
  http<{ status: string }>(`/api/libraries/${id}/scan`, { method: "POST" });

// ----- movies -----
export const listMovies = (q: MovieQuery) =>
  http<MovieListResponse>(`/api/movies${qs(q)}`);
export const getMovie = (id: number) => http<MovieDetail>(`/api/movies/${id}`);
// Delete a movie row from the index. Dependent rows are cascaded; attached
// files (if any) are detached back to the unmatched pool, not deleted.
export const deleteMovie = (id: number) =>
  http<void>(`/api/movies/${id}`, { method: "DELETE" });
export const getMovieFacets = (q: MovieQuery) =>
  http<MovieFacets>(`/api/movies/facets${qs(q)}`);
export const getMovieCast = (id: number) =>
  http<CastResponse>(`/api/movies/${id}/cast`);
export const listMovieFiles = (id: number) =>
  http<MovieFile[]>(`/api/movies/${id}/files`);
export const getMovieFile = (movieId: number, fileId: number) =>
  http<MovieFileDetail>(`/api/movies/${movieId}/files/${fileId}`);

// ----- genres / misc -----
export const listGenres = () => http<Genre[]>(`/api/genres`);
export const listCountries = () => http<CountryCount[]>(`/api/countries`);
export const tmdbImage = (path: string, size = "w500") =>
  path ? `https://image.tmdb.org/t/p/${size}${path}` : "";

// ----- collections -----
// minMovies filters the list by local movie count: 2 (default) hides
// single-member collections, 1 includes every collection with at least one film.
export const listCollections = (minMovies = 2) =>
  http<CollectionWithCount[]>(`/api/collections?min_movies=${minMovies}`);
export const getCollection = (id: number) => http<Collection>(`/api/collections/${id}`);
export const listCollectionParts = (id: number) =>
  http<CollectionPart[]>(`/api/collections/${id}/parts`);
export const enrichCollection = (id: number) =>
  http<{ refreshed: boolean; collection: Collection }>(`/api/collections/${id}/enrich`, {
    method: "POST",
  });
// enrichAllCollections triggers an async batch TMDB refresh of every
// collection's metadata + member list. Returns immediately with "started";
// the result is logged server-side. 409 means one is already running.
export const enrichAllCollections = () =>
  http<{ status: string }>(`/api/collections/enrich-all`, { method: "POST" });

// ----- SQL console -----
export const listTables = () => http<TableInfo[]>("/api/dev/tables");
export const execSQL = (sql: string, write: boolean) =>
  http<SqlResult>("/api/dev/sql", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ sql, write }),
  });

// ----- manual matching & rename -----
export const searchCandidates = (q: string, movieId?: number, year?: number) =>
  http<CandidatesResponse>(
    `/api/movies/${movieId ?? 0}/candidates?${new URLSearchParams({
      q,
      ...(movieId ? { movie_id: String(movieId) } : {}),
      ...(year ? { year: String(year) } : {}),
    })}`
  );
export const matchMovie = (movieId: number, tmdbId: number) =>
  http<MatchResponse>(`/api/movies/${movieId}/match`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tmdb_id: tmdbId }),
  });
export const unmatchMovie = (movieId: number) =>
  http<{ movie_id: number; status: string }>(`/api/movies/${movieId}/unmatch`, {
    method: "POST",
  });
export const renameMovieFile = (movieId: number, fileId: number, dryRun: boolean) =>
  http<RenameResponse>(
    `/api/movies/${movieId}/rename?${new URLSearchParams({
      file_id: String(fileId),
      dry_run: dryRun ? "1" : "0",
    })}`,
    { method: "POST" }
  );

// Upload subtitle files for one release. langs must be parallel to files.
export const uploadSubtitles = async (
  movieId: number,
  fileId: number,
  files: File[],
  langs: string[]
): Promise<{ uploaded: SubtitleUploadResult[] }> => {
  const form = new FormData();
  files.forEach((f) => form.append("files", f));
  langs.forEach((l) => form.append("langs", l));
  const res = await fetch(
    `/api/movies/${movieId}/files/${fileId}/subtitles`,
    { method: "POST", body: form }
  );
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`${res.status} ${res.statusText} ${text}`);
  }
  return res.json();
};
