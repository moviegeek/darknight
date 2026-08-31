import { useMemo } from "react";
import { useParams, Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, RefreshCw } from "lucide-react";

import {
  getCollection,
  listCollectionParts,
  listMovies,
  enrichCollection,
  tmdbImage,
} from "../api/client";
import { CardSizeSlider } from "../components/CardSizeSlider";
import { MovieCard } from "../components/MovieCard";
import { useCardSize } from "../lib/useCardSize";
import { useShowMissing } from "../lib/useShowMissing";
import { cn } from "../lib/format";
import type { CollectionPart, MovieListItem } from "../api/types";

export default function CollectionPage() {
  const { id } = useParams<{ id: string }>();
  const collectionId = Number(id);
  const qc = useQueryClient();
  // card size is the persisted global preference shared with the library page.
  const [cardSize, setCardSize] = useCardSize();
  // showMissing is a global preference (toggled on the collections list page)
  // applied to every detail page; it is persisted in localStorage so it
  // survives navigation.
  const [showMissing] = useShowMissing();

  const { data: collection } = useQuery({
    queryKey: ["collection", collectionId],
    queryFn: () => getCollection(collectionId),
    enabled: !!collectionId,
  });
  const { data: parts = [], isLoading: partsLoading } = useQuery({
    queryKey: ["collection-parts", collectionId],
    queryFn: () => listCollectionParts(collectionId),
    enabled: !!collectionId,
  });
  // owned films with their local file badges (resolution/HDR/...). We map by
  // tmdb_id so each owned part can render via the same MovieCard as the library.
  const { data: moviesResp } = useQuery({
    queryKey: ["collection-movies", collectionId],
    queryFn: () => listMovies({ collection: collectionId, sort: "year", limit: 0 }),
    enabled: !!collectionId,
  });
  const moviesByTmdbId = useMemo(() => {
    const m = new Map<number, MovieListItem>();
    for (const item of moviesResp?.items ?? []) {
      if (item.tmdb_id) m.set(item.tmdb_id, item);
    }
    return m;
  }, [moviesResp]);

  const refreshMut = useMutation({
    mutationFn: () => enrichCollection(collectionId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["collection", collectionId] });
      qc.invalidateQueries({ queryKey: ["collection-parts", collectionId] });
      qc.invalidateQueries({ queryKey: ["collection-movies", collectionId] });
      qc.invalidateQueries({ queryKey: ["collections"] });
    },
  });

  if (!collection) {
    return <div className="py-24 text-center text-ink-dim">加载中…</div>;
  }

  const backdrop = tmdbImage(collection.backdrop_path, "w1280");
  const poster = tmdbImage(collection.poster_path, "w500");

  const ownedCount = parts.filter((p) => p.local_movie_id).length;
  const totalCount = parts.length;
  // the visible list: all parts when showMissing is on, otherwise only owned.
  const visible = showMissing ? parts : parts.filter((p) => p.local_movie_id);

  return (
    <div>
      {/* backdrop header */}
      <div className="relative h-56 overflow-hidden border-b border-border bg-bg-panel">
        {backdrop && (
          <img src={backdrop} alt="" className="h-full w-full object-cover opacity-40" />
        )}
        <div className="absolute inset-0 bg-gradient-to-t from-bg to-transparent" />
        <div className="absolute left-6 top-4">
          <Link
            to="/collections"
            className="inline-flex items-center gap-1 text-sm text-ink-muted hover:text-ink"
          >
            <ArrowLeft className="h-4 w-4" />
            返回合集
          </Link>
        </div>
      </div>

      <div className="mx-auto max-w-[1400px] px-6">
        {/* title row */}
        <div className="-mt-28 flex items-start gap-6">
          <div className="w-36 shrink-0 overflow-hidden rounded-lg bg-bg-card shadow-lg">
            <div className="relative aspect-[2/3] w-full overflow-hidden bg-bg">
              {poster ? (
                <img src={poster} alt={collection.name} className="h-full w-full object-cover" />
              ) : (
                <div className="flex h-full w-full items-center justify-center p-2 text-center text-xs text-ink-dim">
                  {collection.name}
                </div>
              )}
            </div>
          </div>
          <div className="flex-1 pt-28">
            <div className="flex items-start justify-between gap-4">
              <div>
                <h1 className="text-3xl font-bold text-ink">{collection.name}</h1>
                <p className="mt-1 text-sm text-ink-muted">
                  {totalCount > 0
                    ? `已入库 ${ownedCount} / 共 ${totalCount} 部`
                    : `已入库 ${ownedCount} 部`}
                </p>
              </div>
              <button
                onClick={() => refreshMut.mutate()}
                disabled={refreshMut.isPending}
                title="从 TMDB 刷新合集元数据与成员列表"
                className="inline-flex shrink-0 items-center gap-1 rounded-md border border-border px-2.5 py-1.5 text-xs text-ink-muted hover:text-ink disabled:opacity-50"
              >
                <RefreshCw className={cn("h-3.5 w-3.5", refreshMut.isPending && "animate-spin")} />
                刷新元数据
              </button>
            </div>
            {collection.overview && (
              <p className="mt-4 max-w-3xl text-sm leading-relaxed text-ink-muted">
                {collection.overview}
              </p>
            )}
            {refreshMut.isError && (
              <p className="mt-2 text-xs text-accent">刷新失败：{String(refreshMut.error)}</p>
            )}
            {refreshMut.isSuccess && refreshMut.data.refreshed && (
              <p className="mt-2 text-xs text-emerald-400">已从 TMDB 刷新。</p>
            )}
            {refreshMut.isSuccess && !refreshMut.data.refreshed && (
              <p className="mt-2 text-xs text-ink-dim">未刷新（无 TMDB id 或未匹配）。</p>
            )}
          </div>
        </div>

        {/* movies grid */}
        <section className="mt-8 pb-12">
          <div className="mb-4 flex items-center justify-between gap-4">
            <h2 className="text-lg font-semibold text-ink">收录电影</h2>
            <CardSizeSlider size={cardSize} onChange={setCardSize} />
          </div>

          {totalCount === 0 ? (
            // never refreshed against TMDB: fall back to owned films only.
            <OwnedOnlyGrid movies={moviesResp?.items ?? []} size={cardSize} loading={partsLoading} />
          ) : visible.length === 0 ? (
            <p className="text-sm text-ink-muted">
              {showMissing ? "该合集暂无成员影片。" : "该合集在库里还没有电影。"}
            </p>
          ) : (
            <div
              className="grid gap-4"
              style={{ gridTemplateColumns: `repeat(auto-fill, minmax(${cardSize}px, 1fr))` }}
            >
              {visible.map((p) =>
                p.local_movie_id ? (
                  <OwnedPartCard
                    key={p.id}
                    part={p}
                    movie={moviesByTmdbId.get(p.tmdb_id)}
                    size={cardSize}
                  />
                ) : (
                  <MissingPartCard key={p.id} part={p} size={cardSize} />
                )
              )}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

// OwnedPartCard renders a film that is in the library. When we have the
// matching MovieListItem (with file badges), we reuse MovieCard for parity with
// the library grid; otherwise we fall back to a TMDB-only card.
function OwnedPartCard({
  part,
  movie,
  size,
}: {
  part: CollectionPart;
  movie?: MovieListItem;
  size: number;
}) {
  if (movie) {
    return <MovieCard movie={movie} size={size} />;
  }
  return <TmdbOnlyCard part={part} size={size} />;
}

// MissingPartCard renders a film that is NOT in the library: greyscale poster,
// reduced opacity, a "缺失" corner badge, and no link (nothing to navigate to).
function MissingPartCard({ part, size }: { part: CollectionPart; size: number }) {
  const poster = tmdbImage(part.poster_path, "w500");
  const title = part.original_title || part.title || "（无标题）";
  const year = part.release_date ? part.release_date.slice(0, 4) : "";
  return (
    <div
      className="group block overflow-hidden rounded-lg bg-bg-card opacity-70"
      style={{ maxWidth: size }}
      title="库里还没有这部电影"
    >
      <div className="relative aspect-[2/3] overflow-hidden bg-bg">
        {poster ? (
          <img
            src={poster}
            alt={title}
            loading="lazy"
            className="h-full w-full object-cover grayscale"
          />
        ) : (
          <div className="flex h-full w-full items-center justify-center p-2 text-center text-xs text-ink-dim">
            {title}
          </div>
        )}
        <span className="absolute right-2 top-2 rounded bg-accent/80 px-1.5 py-0.5 text-[11px] font-medium text-white">
          缺失
        </span>
      </div>
      <div className="p-2.5">
        <div className="truncate font-medium text-ink-muted" title={title}>
          {title}
        </div>
        <div className="mt-0.5 flex items-center gap-2 text-xs text-ink-dim">
          {year && <span>{year}</span>}
          {part.vote_average > 0 && (
            <span className="text-amber-400/70">★ {part.vote_average.toFixed(1)}</span>
          )}
        </div>
      </div>
    </div>
  );
}

// TmdbOnlyCard: an owned film whose local MovieListItem isn't loaded (e.g. the
// movies query is still in flight). Renders the TMDB poster so the grid doesn't
// pop; badges fill in once the movies query resolves via OwnedPartCard.
function TmdbOnlyCard({ part, size }: { part: CollectionPart; size: number }) {
  const poster = tmdbImage(part.poster_path, "w500");
  const title = part.original_title || part.title || "（无标题）";
  const year = part.release_date ? part.release_date.slice(0, 4) : "";
  return (
    <Link
      to={`/movie/${part.local_movie_id}`}
      className="group block overflow-hidden rounded-lg bg-bg-card transition hover:bg-bg-hover"
      style={{ maxWidth: size }}
    >
      <div className="relative aspect-[2/3] overflow-hidden bg-bg">
        {poster ? (
          <img src={poster} alt={title} loading="lazy" className="h-full w-full object-cover" />
        ) : (
          <div className="flex h-full w-full items-center justify-center p-2 text-center text-xs text-ink-dim">
            {title}
          </div>
        )}
      </div>
      <div className="p-2.5">
        <div className="truncate font-medium text-ink" title={title}>
          {title}
        </div>
        {year && <div className="mt-0.5 text-xs text-ink-muted">{year}</div>}
      </div>
    </Link>
  );
}

// OwnedOnlyGrid is the fallback for collections never refreshed against TMDB
// (no parts cached): show the owned films directly, like the old detail page.
function OwnedOnlyGrid({
  movies,
  size,
  loading,
}: {
  movies: MovieListItem[];
  size: number;
  loading: boolean;
}) {
  if (loading) {
    return <div className="py-24 text-center text-ink-dim">加载中…</div>;
  }
  if (movies.length === 0) {
    return <p className="text-sm text-ink-muted">该合集在库里还没有电影。</p>;
  }
  return (
    <div
      className="grid gap-4"
      style={{ gridTemplateColumns: `repeat(auto-fill, minmax(${size}px, 1fr))` }}
    >
      {movies.map((m) => (
        <MovieCard key={m.id} movie={m} size={size} />
      ))}
    </div>
  );
}
