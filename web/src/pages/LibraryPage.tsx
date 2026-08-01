import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { getMovieFacets, listCountries, listMovies } from "../api/client";
import type { MovieQuery } from "../api/types";
import { FilterPanel } from "../components/FilterPanel";
import { MovieCard } from "../components/MovieCard";
import { useLibraryQuery } from "../lib/useLibraryQuery";

export default function LibraryPage() {
  // The query is persisted to localStorage so the user's selected filters and
  // sort survive opening a movie's detail page and navigating back (which
  // unmounts this component and would otherwise reset useState).
  const [query, setQuery] = useLibraryQuery();
  const [cardSize, setCardSize] = useState(160);
  const [showCounts, setShowCounts] = useState(true);

  const { data, isLoading, error } = useQuery({
    queryKey: ["movies", query],
    queryFn: () => listMovies(query),
  });
  const { data: facets } = useQuery({
    queryKey: ["movie-facets", query],
    queryFn: () => getMovieFacets(query),
    enabled: showCounts,
  });
  // unfiltered per-country movie counts, used to decide which countries get
  // their own chip vs. folding into "其他" - this shouldn't reshuffle as the
  // user changes other filters, so it's fetched once, not keyed on `query`.
  const { data: countries = [] } = useQuery({
    queryKey: ["countries"],
    queryFn: listCountries,
  });

  const items = data?.items ?? [];
  const total = data?.total ?? 0;

  // quick-access chips at the top of the grid
  const quickChips = useMemo(
    () =>
      [
        { label: "4K HDR", patch: { resolution: "2160p", hdr: "HDR10" } },
        { label: "Dolby Vision", patch: { dolby_vision: true } },
        { label: "未看", patch: { watched: "unwatched" } },
        { label: "高分 ★8+", patch: {} }, // vote filter not yet in API; placeholder
      ] as Array<{ label: string; patch: Partial<MovieQuery> }>,
    []
  );

  return (
    <div className="flex">
      <FilterPanel
        query={query}
        onChange={setQuery}
        total={total}
        facets={facets}
        showCounts={showCounts}
        onToggleCounts={setShowCounts}
        countries={countries}
      />

      <div className="flex-1 px-6 py-5">
        {/* toolbar: quick chips + card-size slider */}
        <div className="mb-4 flex flex-wrap items-center gap-4">
          <div className="flex flex-wrap items-center gap-2">
            {quickChips.map((qc) => {
              const active =
                (qc.patch.resolution && query.resolution === qc.patch.resolution) ||
                (qc.patch.dolby_vision && query.dolby_vision) ||
                (qc.patch.watched && query.watched === qc.patch.watched);
              return (
                <button
                  key={qc.label}
                  onClick={() =>
                    setQuery({
                      ...query,
                      resolution: undefined,
                      hdr: undefined,
                      dolby_vision: undefined,
                      watched: undefined,
                      offset: 0,
                      ...(qc.patch as MovieQuery),
                    })
                  }
                  className={"chip " + (active ? "chip-active" : "")}
                >
                  {qc.label}
                </button>
              );
            })}
          </div>

          <CardSizeSlider size={cardSize} onChange={setCardSize} />
        </div>

        {error && (
          <div className="rounded-md border border-accent/40 bg-accent/10 px-4 py-3 text-sm text-accent">
            加载失败：{String(error)}
          </div>
        )}

        {isLoading ? (
          <div className="py-24 text-center text-ink-dim">加载中…</div>
        ) : items.length === 0 ? (
          <EmptyState />
        ) : (
          <>
            <div
              className="grid gap-4"
              style={{
                gridTemplateColumns: `repeat(auto-fill, minmax(${cardSize}px, 1fr))`,
              }}
            >
              {items.map((m) => (
                <MovieCard key={m.id} movie={m} size={cardSize} />
              ))}
            </div>

            <div className="mt-6 text-center text-xs text-ink-dim">
              共 {total} 部
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function CardSizeSlider({
  size,
  onChange,
}: {
  size: number;
  onChange: (size: number) => void;
}) {
  return (
    <div className="flex items-center gap-3 rounded-md border border-border bg-bg-panel px-3 py-1.5 text-xs text-ink-muted">
      <span>小</span>
      <input
        type="range"
        min={100}
        max={320}
        step={10}
        value={size}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-32 accent-accent sm:w-40"
      />
      <span>大</span>
      <span className="ml-1 w-10 text-right tabular-nums text-ink">{size}px</span>
    </div>
  );
}

function EmptyState() {
  return (
    <div className="py-24 text-center">
      <p className="text-lg text-ink">资料库是空的</p>
      <p className="mt-2 text-sm text-ink-muted">
        前往「设置」添加一个媒体库目录并触发扫描。
      </p>
    </div>
  );
}
