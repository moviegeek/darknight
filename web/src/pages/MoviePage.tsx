import { useParams, Link } from "react-router-dom";
import { useQueries, useQuery } from "@tanstack/react-query";
import { ArrowLeft, Disc3, FileText, FileVideo } from "lucide-react";

import {
  getMovie,
  getMovieCast,
  listMovieFiles,
  getMovieFile,
  tmdbImage,
} from "../api/client";
import type { AudioTrack, CastResponse, MovieFile, MovieFileDetail, Subtitle } from "../api/types";
import { ManualMatchButton } from "../components/ManualMatch";
import {
  cn,
  audioChannelCountLabel,
  countryFlagEmoji,
  formatBitrate,
  formatBytes,
  formatDuration,
  movieCountryCodes,
  resolutionLabel,
  movieTitleLines,
  sourceLabel,
} from "../lib/format";

export default function MoviePage() {
  const { id } = useParams<{ id: string }>();
  const movieId = Number(id);

  const { data: movie } = useQuery({
    queryKey: ["movie", movieId],
    queryFn: () => getMovie(movieId),
    enabled: !!movieId,
  });
  const { data: files = [] } = useQuery({
    queryKey: ["movie-files", movieId],
    queryFn: () => listMovieFiles(movieId),
    enabled: !!movieId,
  });
  const { data: cast } = useQuery({
    queryKey: ["movie-cast", movieId],
    queryFn: () => getMovieCast(movieId),
    enabled: !!movieId,
  });

  // fetch the full detail (audio/subs) for each file in parallel
  const details = useQueries({
    queries: files.map((f) => ({
      queryKey: ["movie-file", movieId, f.id],
      queryFn: () => getMovieFile(movieId, f.id),
      enabled: files.length > 0,
    })),
  });

  if (!movie) {
    return <div className="py-24 text-center text-ink-dim">加载中…</div>;
  }

  const backdrop = tmdbImage(movie.backdrop_path, "w1280");
  const poster = tmdbImage(movie.poster_path, "w500");
  const { primary, secondary } = movieTitleLines(movie);

  return (
    <div>
      {/* backdrop header */}
      <div className="relative h-64 overflow-hidden border-b border-border bg-bg-panel">
        {backdrop && (
          <img src={backdrop} alt="" className="h-full w-full object-cover opacity-40" />
        )}
        <div className="absolute inset-0 bg-gradient-to-t from-bg to-transparent" />
        <div className="absolute left-6 top-4">
          <Link
            to="/"
            className="inline-flex items-center gap-1 text-sm text-ink-muted hover:text-ink"
          >
            <ArrowLeft className="h-4 w-4" />
            返回资料库
          </Link>
        </div>
      </div>

      <div className="mx-auto max-w-[1400px] px-6">
        {/* title row */}
        <div className="-mt-32 flex items-start gap-6">
          <div className="w-40 shrink-0 overflow-hidden rounded-lg bg-bg-card shadow-lg">
            <div className="relative aspect-[2/3] w-full overflow-hidden bg-bg">
              {poster ? (
                <img
                  src={poster}
                  alt={primary}
                  className="h-full w-full object-cover"
                />
              ) : (
                <div className="flex h-full w-full items-center justify-center text-center text-xs text-ink-dim">
                  {primary}
                </div>
              )}
            </div>
          </div>
          <div className="flex-1 pt-32">
            <div className="flex items-start justify-between gap-4">
              <div>
                <h1 className="text-3xl font-bold text-ink">
                  {primary}
                  {movie.year ? <span className="ml-2 text-ink-muted">({movie.year})</span> : null}
                </h1>
                {secondary && <p className="mt-1 text-base text-ink-muted">{secondary}</p>}
              </div>
              <div className="shrink-0 pt-1">
                <ManualMatchButton movie={movie} files={files} />
              </div>
            </div>
            {!movie.tmdb_id && (
              <div className="mt-3 rounded-md border border-rose-800 bg-rose-950/30 px-3 py-2 text-sm text-rose-200">
                此电影缺少 TMDB 信息（无封面/简介）。点击右上角「手动匹配」搜索并修复。
              </div>
            )}
            <div className="mt-2 flex flex-wrap items-center gap-3 text-sm text-ink-muted">
              {movie.runtime > 0 && <span>{formatDuration(movie.runtime * 60)}</span>}
              {movie.vote_average > 0 && (
                <span className="text-amber-400">★ {movie.vote_average.toFixed(1)}</span>
              )}
              {movieCountryCodes(movie).length > 0 && (
                <span title={movieCountryCodes(movie).join(", ")}>
                  {movieCountryCodes(movie)
                    .map((c) => countryFlagEmoji(c))
                    .join(" ")}
                </span>
              )}
              {movie.imdb_id && (
                <a
                  href={`https://www.imdb.com/title/${movie.imdb_id}`}
                  target="_blank"
                  rel="noreferrer"
                  className="hover:text-ink"
                >
                  {movie.imdb_id}
                </a>
              )}
            </div>
            {movie.genres && movie.genres.length > 0 && (
              <div className="mt-3 flex flex-wrap gap-1.5">
                {movie.genres.map((g) => (
                  <span key={g.id} className="badge bg-bg-card text-ink-muted">
                    {g.name}
                  </span>
                ))}
              </div>
            )}
            {movie.synopsis && (
              <p className="mt-4 max-w-3xl text-sm leading-relaxed text-ink-muted">
                {movie.synopsis}
              </p>
            )}
          </div>
        </div>

        {/* cast */}
        {cast && cast.cast.length > 0 && (
          <CastRow cast={cast} />
        )}

        {/* version comparison */}
        <section className="mt-10">
          <h2 className="mb-3 text-lg font-semibold text-ink">
            文件版本 <span className="text-ink-dim">({files.length})</span>
          </h2>
          {files.length === 0 ? (
            <p className="text-sm text-ink-muted">暂无文件。</p>
          ) : (
            <>
              <VersionTable files={files} details={details.map((d) => d.data)} />
              {/* file paths */}
              <div className="mt-4 space-y-4">
                {files.map((f, i) => (
                  <FilePaths key={f.id} file={f} detail={details[i]?.data} />
                ))}
              </div>
            </>
          )}
        </section>
      </div>
    </div>
  );
}

function VersionTable({
  files,
  details,
}: {
  files: ReturnType<typeof listMovieFiles> extends Promise<infer T> ? T : never;
  details: Array<MovieFileDetail | undefined>;
}) {
  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full text-sm">
        <thead className="bg-bg-panel text-left text-xs uppercase tracking-wide text-ink-dim">
          <tr>
            <Th>类型</Th>
            <Th>分辨率</Th>
            <Th>来源</Th>
            <Th>编码</Th>
            <Th>音频</Th>
            <Th>HDR</Th>
            <Th>大小</Th>
            <Th>码率</Th>
            <Th>时长</Th>
            <Th>Group</Th>
            <Th>音轨</Th>
            <Th>内嵌字幕</Th>
            <Th>外挂字幕</Th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {files.map((f, i) => {
            const detail = details[i];
            return (
              <tr key={f.id} className="text-ink-muted hover:bg-bg-card">
                <Td>
                  <span className="inline-flex items-center gap-1">
                    {f.is_disc ? (
                      <>
                        <Disc3 className="h-4 w-4 text-emerald-400" /> 原盘
                      </>
                    ) : (
                      <>
                        <FileVideo className="h-4 w-4 text-blue-400" /> mkv
                      </>
                    )}
                  </span>
                </Td>
                <Td className="text-ink">{resolutionLabel(f.resolution) || "—"}</Td>
                <Td>{sourceLabel(f.source)}</Td>
                <Td>{f.video_codec || "—"}</Td>
                <Td>
                  {f.audio_codec || "—"}
                  {f.audio_channels && <span className="text-ink-dim"> {f.audio_channels}</span>}
                </Td>
                <Td>
                  {f.hdr && <span className="badge badge-hdr">{f.hdr}</span>}
                  {f.dolby_vision && <span className="badge badge-dv ml-1">DV</span>}
                  {!f.hdr && !f.dolby_vision && "—"}
                </Td>
                <Td>{formatBytes(f.file_size)}</Td>
                <Td>{formatBitrate(f.video_bitrate)}</Td>
                <Td>{formatDuration(f.duration_sec)}</Td>
                <Td>{f.release_group || "—"}</Td>
                <Td>
                  <AudioTrackList tracks={detail?.audio_tracks} />
                </Td>
                <Td>
                  <SubtitleList subs={detail?.subtitles} embedded />
                </Td>
                <Td>
                  <SubtitleList subs={detail?.subtitles} embedded={false} />
                </Td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// FilePaths renders the video file path and any external subtitle file paths
// for one release as a compact, standalone block below the version table.
// Each path is shown as {library}/dir/filename in monospace.
function FilePaths({ file, detail }: { file: MovieFile; detail?: MovieFileDetail }) {
  const videoPath = [file.library_name, file.dir_path, file.is_disc ? "" : file.file_name]
    .filter(Boolean)
    .join("/");
  const extSubs = (detail?.subtitles ?? []).filter((s) => !s.is_embedded);
  return (
    <div className="space-y-1">
      <div className="flex items-start gap-1.5 font-mono text-xs text-ink-dim">
        <FileVideo className="mt-0.5 h-3 w-3 shrink-0" />
        <span className="break-all" title={videoPath}>
          {videoPath}
        </span>
      </div>
      {extSubs.map((s) => {
        const name = s.file_path.split("/").pop() || s.file_path;
        const subPath = [file.library_name, file.dir_path, name].filter(Boolean).join("/");
        return (
          <div
            key={s.id}
            className="flex items-start gap-1.5 font-mono text-xs text-ink-dim"
          >
            <FileText className="mt-0.5 h-3 w-3 shrink-0" />
            <span className="break-all" title={subPath}>
              {subPath}
            </span>
          </div>
        );
      })}
    </div>
  );
}

// AudioTrackList shows one line per audio stream: language, codec, channel
// layout, and default/lossless flags.
function AudioTrackList({ tracks }: { tracks?: AudioTrack[] }) {
  if (!tracks) return <span className="text-xs text-ink-dim">…</span>;
  if (tracks.length === 0) return <span className="text-xs text-ink-dim">—</span>;
  return (
    <ul className="space-y-0.5 text-xs">
      {tracks.map((t) => (
        <li key={t.id}>
          <span className="text-ink">{t.language || "und"}</span>
          <span className="text-ink-dim"> · </span>
          {t.codec || "—"}
          {audioChannelCountLabel(t.channels) && ` ${audioChannelCountLabel(t.channels)}`}
          {t.is_lossless && <span className="ml-1 text-emerald-400">无损</span>}
          {t.is_default && <span className="ml-1 text-ink-dim">默认</span>}
          {t.title && <span className="ml-1 text-ink-dim">({t.title})</span>}
        </li>
      ))}
    </ul>
  );
}

// SubtitleList renders one column's worth of subtitle streams: either the
// embedded ones (language + format) or the external files (language, format,
// and file size), depending on `embedded`.
function SubtitleList({
  subs,
  embedded,
}: {
  subs?: Subtitle[];
  embedded: boolean;
}) {
  if (!subs) return <span className="text-xs text-ink-dim">…</span>;
  const list = subs.filter((s) => s.is_embedded === embedded);
  if (list.length === 0) return <span className="text-xs text-ink-dim">-</span>;
  return (
    <ul className="space-y-0.5 text-xs">
      {list.map((s) => (
        <li key={s.id}>
          <span className="text-ink">{s.language || "und"}</span>
          <span className="text-ink-dim"> · {s.format || "-"}</span>
          {embedded && s.is_default && <span className="ml-1 text-ink-dim">默认</span>}
          {!embedded && s.file_size > 0 && (
            <span className="ml-1 text-ink-dim">({formatBytes(s.file_size)})</span>
          )}
        </li>
      ))}
    </ul>
  );
}

function Th({ children }: { children: React.ReactNode }) {
  return <th className="px-3 py-2 font-medium">{children}</th>;
}
function Td({ children, className }: { children: React.ReactNode; className?: string }) {
  return <td className={cn("px-3 py-2", className)}>{children}</td>;
}

// CastRow renders the top-billed actors + director(s) as a horizontal scroller
// of profile cards. TMDB profile images are served from its CDN.
function CastRow({ cast }: { cast: CastResponse }) {
  const people = cast.people ?? {};
  const actors = [...cast.cast]
    .sort((a, b) => a.order - b.order)
    .slice(0, 12)
    .map((c) => ({ c, p: people[c.person_id] }))
    .filter((x) => x.p);
  const directors = cast.crew.filter((c) => c.job === "Director");
  if (actors.length === 0 && directors.length === 0) return null;

  return (
    <section className="mt-8">
      <h2 className="mb-3 text-lg font-semibold text-ink">演职员</h2>
      {directors.length > 0 && (
        <p className="mb-3 text-sm text-ink-muted">
          导演：
          {directors.map((d, i) => (
            <span key={d.person_id}>
              {i > 0 && "、"}
              {people[d.person_id]?.name ?? "—"}
            </span>
          ))}
        </p>
      )}
      {actors.length > 0 && (
        <div className="flex gap-3 overflow-x-auto pb-2">
          {actors.map(({ c, p }) => {
            const img = tmdbImage(p.profile_path, "w185");
            return (
              <div key={c.person_id} className="w-24 shrink-0 text-center">
                <div className="mx-auto mb-1.5 h-24 w-24 overflow-hidden rounded-full bg-bg-card">
                  {img ? (
                    <img src={img} alt={p.name} loading="lazy" className="h-full w-full object-cover" />
                  ) : (
                    <div className="flex h-full w-full items-center justify-center text-2xl text-ink-dim">
                      {p.name?.[0] ?? "?"}
                    </div>
                  )}
                </div>
                <div className="truncate text-xs font-medium text-ink" title={p.name}>
                  {p.name}
                </div>
                {c.character && (
                  <div className="truncate text-[11px] text-ink-dim" title={c.character}>
                    {c.character}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
