import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { cn, resolutionLabel, movieTitleLines, movieCountryCodes, countryFlagEmoji } from "../lib/format";
import type { MovieListItem } from "../api/types";
import { tmdbImage } from "../api/client";

// Country flag(s) for a movie card: the main (first) country as a flag emoji,
// plus a "..." toggle that reveals the rest when there's more than one.
function CountryBadge({ movie }: { movie: Pick<MovieListItem, "country" | "countries"> }) {
  const codes = movieCountryCodes(movie);
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDocClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [open]);

  if (codes.length === 0) return null;

  return (
    <div ref={ref} className="relative flex items-center gap-1">
      <span title={codes[0]}>{countryFlagEmoji(codes[0])}</span>
      {codes.length > 1 && (
        <>
          <button
            type="button"
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              setOpen((v) => !v);
            }}
            className="rounded px-1 leading-none text-ink-muted hover:text-ink"
            aria-label="show all countries"
          >
            ...
          </button>
          {open && (
            <div
              className="absolute left-0 top-full z-10 mt-1 flex flex-wrap gap-1 whitespace-nowrap rounded-md bg-bg-card p-2 shadow-lg"
              onClick={(e) => e.preventDefault()}
            >
              {codes.map((c) => (
                <span key={c} title={c} className="text-base">
                  {countryFlagEmoji(c)}
                </span>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}

// Technical badges shown over / under a poster: resolution, HDR, DV, disc, chi sub.
export function TechBadges({
  movie,
  className,
}: {
  movie: Pick<
    MovieListItem,
    "best_resolution" | "best_hdr" | "dolby_vision" | "has_files" | "has_chi_subtitle"
  >;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-wrap gap-1", className)}>
      {movie.best_resolution && (
        <span className="badge badge-res">{resolutionLabel(movie.best_resolution)}</span>
      )}
      {movie.best_hdr && movie.best_hdr !== "DV" && (
        <span className="badge badge-hdr">{movie.best_hdr}</span>
      )}
      {movie.dolby_vision && <span className="badge badge-dv">DV</span>}
      {movie.has_chi_subtitle && <span className="badge badge-sub">中字</span>}
    </div>
  );
}

// Data-health badges: which of the two "unmatched" problem classes this movie
// falls into, and whether several files map to it. Rendered top-right so they
// never overlap the technical badges on the left.
export function HealthBadges({
  movie,
  className,
}: {
  movie: Pick<MovieListItem, "has_files" | "file_count" | "tmdb_id">;
  className?: string;
}) {
  const noFiles = !movie.has_files;
  const noTmdb = !movie.tmdb_id;
  const multi = movie.file_count > 1;
  if (!noFiles && !noTmdb && !multi) return null;
  return (
    <div className={cn("flex flex-wrap gap-1", className)}>
      {noFiles && (
        <span className="badge badge-warn" title="有电影条目，但没有任何电影文件">
          无文件
        </span>
      )}
      {noTmdb && (
        <span className="badge badge-warn" title="条目信息不全：缺 TMDB id（因此没有封面）">
          缺 TMDB
        </span>
      )}
      {multi && (
        <span className="badge badge-multi" title={`${movie.file_count} 个文件映射到这部电影`}>
          ×{movie.file_count}
        </span>
      )}
    </div>
  );
}

export function MovieCard({ movie, size = 160 }: { movie: MovieListItem; size?: number }) {
  const poster = tmdbImage(movie.poster_path, "w500");
  const { primary, secondary } = movieTitleLines(movie);
  // size is the desired width in px. The card fills its grid cell but never
  // exceeds this width, so the slider effectively controls the grid density.
  return (
    <Link
      to={`/movie/${movie.id}`}
      className="group block overflow-hidden rounded-lg bg-bg-card transition hover:bg-bg-hover"
      style={{ maxWidth: size }}
    >
      <div className="relative aspect-[2/3] overflow-hidden bg-bg">
        {poster ? (
          <img
            src={poster}
            alt={primary}
            loading="lazy"
            className="h-full w-full object-cover transition group-hover:scale-105"
          />
        ) : (
          <div className="flex h-full w-full items-center justify-center px-2 text-center text-xs text-ink-dim">
            {primary}
            {movie.year ? ` (${movie.year})` : ""}
          </div>
        )}
        <div className="absolute left-2 top-2">
          <TechBadges movie={movie} className="flex-col" />
        </div>
        <div className="absolute right-2 top-2">
          <HealthBadges movie={movie} className="flex-col items-end" />
        </div>
      </div>
      <div className="p-2.5">
        <div
          className={cn(
            "truncate font-medium text-ink",
            size < 140 ? "text-[11px]" : size < 220 ? "text-sm" : "text-base"
          )}
          title={primary}
        >
          {primary}
        </div>
        {secondary && (
          <div className="mt-0.5 truncate text-xs text-ink-dim" title={secondary}>
            {secondary}
          </div>
        )}
        <div className="mt-0.5 flex items-center gap-2 text-xs text-ink-muted">
          {movie.year ? <span>{movie.year}</span> : null}
          {movie.vote_average > 0 && (
            <span className="text-amber-400">★ {movie.vote_average.toFixed(1)}</span>
          )}
          <CountryBadge movie={movie} />
        </div>
      </div>
    </Link>
  );
}
