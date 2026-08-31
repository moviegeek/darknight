import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, Layers, RefreshCw } from "lucide-react";

import { enrichAllCollections, listCollections, tmdbImage } from "../api/client";
import { CardSizeSlider } from "../components/CardSizeSlider";
import { useCardSize } from "../lib/useCardSize";
import { useShowMissing } from "../lib/useShowMissing";
import { cn } from "../lib/format";
import type { CollectionWithCount } from "../api/types";

export default function CollectionsPage() {
  const qc = useQueryClient();
  // card size is the persisted global preference shared with the library page.
  const [cardSize, setCardSize] = useCardSize();
  // includeSingles=false hides collections with <=1 local movie by default.
  // Toggling it on sends min_movies=1 to include those lone-member collections.
  const [includeSingles, setIncludeSingles] = useState(false);
  // showMissing is the global preference for the collection detail pages: when
  // on, every collection page renders its missing films interleaved with the
  // owned ones. Toggled here so it applies to all detail pages at once.
  const [showMissing, setShowMissing] = useShowMissing();

  // Batch refresh of every collection's TMDB metadata + member list. Runs
  // async server-side; we keep the button in a "running" state and poll the
  // list until it settles. 409 = one is already running.
  const refreshAllMut = useMutation({
    mutationFn: enrichAllCollections,
    onSuccess: () => {
      // give the server a moment then refresh; polling below keeps counts live
      setTimeout(() => qc.invalidateQueries({ queryKey: ["collections"] }), 2000);
    },
    onError: () => {
      setTimeout(() => qc.invalidateQueries({ queryKey: ["collections"] }), 2000);
    },
  });

  const { data: collections = [], isLoading, error } = useQuery({
    queryKey: ["collections", includeSingles],
    queryFn: () => listCollections(includeSingles ? 1 : 2),
    // when a batch refresh is running, poll so the badge counts (total_parts)
    // update as collections finish refreshing on the server.
    refetchInterval: refreshAllMut.isPending ? 3000 : false,
  });

  return (
    <div className="px-6 py-5">
      <div className="mb-4 flex items-center justify-between gap-4">
        <h1 className="text-xl font-semibold text-ink">
          合集 <span className="text-ink-dim">({collections.length})</span>
        </h1>
        <div className="flex items-center gap-4">
          <button
            onClick={() => refreshAllMut.mutate()}
            disabled={refreshAllMut.isPending}
            title="从 TMDB 批量刷新所有合集的元数据与成员列表（需要配置 TMDB_API_KEY）"
            className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs text-ink-muted hover:text-ink disabled:opacity-50"
          >
            <RefreshCw className={cn("h-3.5 w-3.5", refreshAllMut.isPending && "animate-spin")} />
            {refreshAllMut.isPending ? "刷新中…" : "刷新所有合集"}
          </button>
          <button
            onClick={() => setIncludeSingles((v) => !v)}
            title={includeSingles ? "当前：显示全部合集" : "当前：隐藏只含 1 部电影的合集"}
            className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs text-ink-muted hover:text-ink"
          >
            {includeSingles ? (
              <EyeOff className="h-3.5 w-3.5" />
            ) : (
              <Eye className="h-3.5 w-3.5" />
            )}
            {includeSingles ? "隐藏单片合集" : "显示单片合集"}
          </button>
          <button
            onClick={() => setShowMissing(!showMissing)}
            title={
              showMissing
                ? "当前：在合集详情页显示缺失影片"
                : "当前：在合集详情页仅显示已入库影片"
            }
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs",
              showMissing
                ? "border-accent/50 text-accent"
                : "border-border text-ink-muted hover:text-ink"
            )}
          >
            {showMissing ? (
              <EyeOff className="h-3.5 w-3.5" />
            ) : (
              <Eye className="h-3.5 w-3.5" />
            )}
            {showMissing ? "隐藏缺失影片" : "显示缺失影片"}
          </button>
          <CardSizeSlider size={cardSize} onChange={setCardSize} />
        </div>
      </div>

      {refreshAllMut.isError && (
        <div className="mb-3 rounded-md border border-accent/40 bg-accent/10 px-4 py-2 text-xs text-accent">
          批量刷新失败：{String(refreshAllMut.error)}
          {/409/.test(String(refreshAllMut.error)) && "（已有刷新任务在运行）"}
        </div>
      )}
      {refreshAllMut.isSuccess && refreshAllMut.isPending === false && (
        <div className="mb-3 rounded-md border border-emerald-500/40 bg-emerald-500/10 px-4 py-2 text-xs text-emerald-400">
          已开始批量刷新，后台正在从 TMDB 拉取，角标会逐步更新。
        </div>
      )}

      <p className={cn("mb-3 text-xs text-ink-dim", collections.length === 0 && "hidden")}>
        {includeSingles
          ? "显示全部含电影的合集"
          : "已隐藏只含 1 部电影的合集，点击「显示单片合集」可查看"}
      </p>

      {error && (
        <div className="rounded-md border border-accent/40 bg-accent/10 px-4 py-3 text-sm text-accent">
          加载失败：{String(error)}
        </div>
      )}

      {isLoading ? (
        <div className="py-24 text-center text-ink-dim">加载中…</div>
      ) : collections.length === 0 ? (
        <EmptyState />
      ) : (
        <div
          className="grid gap-4"
          style={{ gridTemplateColumns: `repeat(auto-fill, minmax(${cardSize}px, 1fr))` }}
        >
          {collections.map((c) => (
            <CollectionCard key={c.id} collection={c} size={cardSize} />
          ))}
        </div>
      )}
    </div>
  );
}

function CollectionCard({ collection, size }: { collection: CollectionWithCount; size: number }) {
  const poster = tmdbImage(collection.poster_path, "w500");
  return (
    <Link
      to={`/collections/${collection.id}`}
      className="group block overflow-hidden rounded-lg bg-bg-card transition hover:bg-bg-hover"
      style={{ maxWidth: size }}
    >
      <div className="relative aspect-[2/3] overflow-hidden bg-bg">
        {poster ? (
          <img
            src={poster}
            alt={collection.name}
            loading="lazy"
            className="h-full w-full object-cover transition group-hover:scale-105"
          />
        ) : (
          <div className="flex h-full w-full items-center justify-center p-3 text-center text-xs text-ink-dim">
            {collection.name}
          </div>
        )}
        <span className="absolute right-2 top-2 rounded bg-bg/80 px-1.5 py-0.5 text-[11px] font-medium text-ink">
          {collection.movie_count}/
          {collection.total_parts || collection.movie_count} 部
        </span>
      </div>
      <div className="p-2.5">
        <div
          className="truncate font-medium text-ink"
          title={collection.name}
        >
          {collection.name}
        </div>
      </div>
    </Link>
  );
}

function EmptyState() {
  return (
    <div className="py-24 text-center">
      <Layers className="mx-auto h-10 w-10 text-ink-dim" />
      <p className="mt-3 text-lg text-ink">还没有合集</p>
      <p className="mt-2 text-sm text-ink-muted">
        完成 TMDB 元数据补充后，带合集标记的电影会自动归类到这里。
      </p>
    </div>
  );
}
