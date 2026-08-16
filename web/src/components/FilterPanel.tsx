import { Eye, EyeOff, RotateCcw, Search, X } from "lucide-react";
import { cn, countryFlagEmoji, sourceLabel } from "../lib/format";
import type { CountryCount, MovieFacets, MovieQuery } from "../api/types";

// The fixed filter dimensions surfaced to the user. Each is a single-select
// chip group; "all" clears it.
const RESOLUTIONS = ["2160p", "1080p", "720p"];
const SOURCES = ["BluRay", "UHD BluRay", "Bluray Disk", "WebDL", "HDTV"];
const CODECS = ["x265", "x264", "AVC", "HEVC"];
const HDRS = ["HDR10", "HDR10+", "DV"];
// Countries get their own chip once the library has at least this many
// movies from them; the rest fold into a single "其他" chip so the panel
// doesn't grow a chip for every one-off country.
const MIN_COUNTRY_CHIP_COUNT = 10;
const SUBTITLE_LANGS: Array<{ value: string; label: string }> = [
  { value: "chi", label: "中文" },
  { value: "eng", label: "英文" },
  { value: "jpn", label: "日文" },
  { value: "kor", label: "韩文" },
];
const SORTS: Array<{ value: string; label: string }> = [
  { value: "title", label: "标题" },
  { value: "year", label: "年份" },
  { value: "vote_average", label: "评分" },
  { value: "added", label: "添加时间" },
];
// Data-health buckets: describe a broken/incomplete local index, not the film.
// "unmatched" is the union of the two problem classes below it, so its count
// is not their sum.
const MATCH_ISSUES: Array<{ value: string; label: string; hint: string }> = [
  { value: "unmatched", label: "有问题", hint: "无文件或缺 TMDB 信息" },
  { value: "no_files", label: "无文件", hint: "有电影条目，但没有任何电影文件" },
  { value: "no_tmdb", label: "缺 TMDB", hint: "有文件但条目信息不全（无 TMDB id，因此无封面）" },
  { value: "multi_version", label: "多版本", hint: "同一部电影有多个文件" },
];
// Match state machine: how this row got its tmdb_id. "pending" rows have
// top-5 candidate lists stored on the row, awaiting manual confirmation;
// "manual" rows are human-confirmed and immune to automatic re-matching.
const MATCH_STATUSES: Array<{ value: string; label: string; hint: string }> = [
  { value: "matched", label: "已匹配", hint: "自动匹配成功，详情完整" },
  { value: "pending", label: "待审核", hint: "matcher 找到候选但分数不够（60-84），已存 top-5 供人工挑" },
  { value: "unmatched", label: "未匹配", hint: "matcher 找不到 60 分以上候选" },
  { value: "manual", label: "手动", hint: "人工指定过，永不被自动覆盖" },
];

export function FilterPanel({
  query,
  onChange,
  total,
  facets,
  showCounts,
  onToggleCounts,
  countries,
}: {
  query: MovieQuery;
  onChange: (q: MovieQuery) => void;
  total: number;
  facets?: MovieFacets;
  showCounts: boolean;
  onToggleCounts: (show: boolean) => void;
  countries: CountryCount[];
}) {
  const set = (patch: Partial<MovieQuery>) => onChange({ ...query, ...patch });
  // reset() clears every filter dimension while keeping sort/order and the
  // limit/offset view state.
  const reset = () =>
    onChange({ sort: query.sort, desc: query.desc, limit: query.limit, offset: 0 });
  const hasFilters =
    query.q !== undefined ||
    query.resolution !== undefined ||
    query.source !== undefined ||
    query.codec !== undefined ||
    query.hdr !== undefined ||
    query.dolby_vision !== undefined ||
    query.country !== undefined ||
    query.subtitle_lang !== undefined ||
    query.external_subtitle !== undefined ||
    query.no_chi_subtitle !== undefined ||
    query.match_issue !== undefined ||
    query.match_status !== undefined ||
    query.watched !== undefined;
  // count(dimension, value) looks up the facet count for one chip; undefined
  // when counts are hidden or the facets request hasn't resolved yet, in
  // which case Chip renders without a "(N)" suffix.
  const count = (dim: keyof MovieFacets, value: string): number | undefined => {
    if (!showCounts || !facets) return undefined;
    const bucket = facets[dim];
    return typeof bucket === "number" ? bucket : bucket?.[value];
  };

  const mainCountries = countries.filter((c) => c.count >= MIN_COUNTRY_CHIP_COUNT);
  const otherCodes = countries.filter((c) => c.count < MIN_COUNTRY_CHIP_COUNT).map((c) => c.code);
  const otherValue = otherCodes.join(",");
  // sum of the individual per-code facet counts for the "其他" bucket - an
  // approximation (a movie with two "other" countries would be counted
  // twice) accurate enough for a chip preview; the actual click applies a
  // proper OR filter server-side so the real result count is exact.
  const otherCount =
    showCounts && facets ? otherCodes.reduce((sum, c) => sum + (facets.country[c] ?? 0), 0) : undefined;

  return (
    <aside className="w-64 shrink-0 space-y-5 border-r border-border bg-bg-panel px-4 py-5">
      {/* search */}
      <div>
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-2 h-4 w-4 text-ink-dim" />
          <input
            value={query.q ?? ""}
            onChange={(e) => set({ q: e.target.value || undefined, offset: 0 })}
            placeholder="搜索标题…"
            className="w-full rounded-md border border-border bg-bg py-1.5 pl-8 pr-7 text-sm outline-none focus:border-ink-dim"
          />
          {query.q && (
            <button
              onClick={() => set({ q: undefined, offset: 0 })}
              className="absolute right-2 top-1.5 text-ink-dim hover:text-ink"
            >
              <X className="h-4 w-4" />
            </button>
          )}
        </div>
        {hasFilters && (
          <button
            onClick={reset}
            className="mt-2 flex w-full items-center justify-center gap-1.5 rounded-md border border-border py-1.5 text-xs text-ink-muted hover:text-ink"
          >
            <RotateCcw className="h-3.5 w-3.5" />
            重置筛选
          </button>
        )}
      </div>

      <button
        onClick={() => onToggleCounts(!showCounts)}
        className="flex w-full items-center justify-center gap-1.5 rounded-md border border-border py-1.5 text-xs text-ink-muted hover:text-ink"
      >
        {showCounts ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
        {showCounts ? "隐藏数量" : "显示数量"}
      </button>

      <FilterGroup label="分辨率">
        {RESOLUTIONS.map((r) => (
          <Chip
            key={r}
            active={query.resolution === r}
            count={count("resolution", r)}
            onClick={() => set({ resolution: query.resolution === r ? undefined : r, offset: 0 })}
          >
            {r === "2160p" ? "4K" : r}
          </Chip>
        ))}
      </FilterGroup>

      <FilterGroup label="来源">
        {SOURCES.map((s) => (
          <Chip
            key={s}
            active={query.source === s}
            count={count("source", s)}
            onClick={() => set({ source: query.source === s ? undefined : s, offset: 0 })}
          >
            {sourceLabel(s)}
          </Chip>
        ))}
      </FilterGroup>

      <FilterGroup label="编码">
        {CODECS.map((c) => (
          <Chip
            key={c}
            active={query.codec === c}
            count={count("video_codec", c)}
            onClick={() => set({ codec: query.codec === c ? undefined : c, offset: 0 })}
          >
            {c}
          </Chip>
        ))}
      </FilterGroup>

      <FilterGroup label="HDR / Dolby Vision">
        {HDRS.map((h) => (
          <Chip
            key={h}
            active={query.hdr === h}
            count={count("hdr", h)}
            onClick={() => set({ hdr: query.hdr === h ? undefined : h, offset: 0 })}
          >
            {h}
          </Chip>
        ))}
        <Chip
          active={!!query.dolby_vision}
          count={count("dolby_vision", "")}
          onClick={() => set({ dolby_vision: !query.dolby_vision, offset: 0 })}
        >
          Dolby Vision
        </Chip>
      </FilterGroup>

      <FilterGroup label="制作国家">
        {mainCountries.map((c) => (
          <Chip
            key={c.code}
            active={query.country === c.code}
            count={count("country", c.code)}
            onClick={() => set({ country: query.country === c.code ? undefined : c.code, offset: 0 })}
          >
            {countryFlagEmoji(c.code)} {c.code}
          </Chip>
        ))}
        {otherCodes.length > 0 && (
          <Chip
            active={query.country === otherValue}
            count={otherCount}
            onClick={() => set({ country: query.country === otherValue ? undefined : otherValue, offset: 0 })}
          >
            其他
          </Chip>
        )}
      </FilterGroup>

      <FilterGroup label="字幕">
        {SUBTITLE_LANGS.map((s) => (
          <Chip
            key={s.value}
            active={query.subtitle_lang === s.value}
            count={count("subtitle_lang", s.value)}
            onClick={() =>
              set({ subtitle_lang: query.subtitle_lang === s.value ? undefined : s.value, offset: 0 })
            }
          >
            {s.label}
          </Chip>
        ))}
        <Chip
          active={!!query.external_subtitle}
          count={count("external_subtitle", "")}
          onClick={() => set({ external_subtitle: !query.external_subtitle, offset: 0 })}
        >
          外挂字幕
        </Chip>
        <Chip
          active={!!query.no_chi_subtitle}
          count={count("no_chi_subtitle", "")}
          onClick={() => set({ no_chi_subtitle: !query.no_chi_subtitle, offset: 0 })}
        >
          无中文字幕
        </Chip>
      </FilterGroup>

      <FilterGroup label="观看状态">
        {(["unwatched", "watching", "watched"] as const).map((w) => (
          <Chip
            key={w}
            active={query.watched === w}
            count={count("watched", w)}
            onClick={() => set({ watched: query.watched === w ? undefined : w, offset: 0 })}
          >
            {w === "unwatched" ? "未看" : w === "watching" ? "在看" : "已看"}
          </Chip>
        ))}
      </FilterGroup>

      <FilterGroup label="匹配状态">
        <div className="mb-1 text-[10px] uppercase tracking-wide text-ink-dim">数据健康</div>
        {MATCH_ISSUES.map((mi) => (
          <Chip
            key={mi.value}
            active={query.match_issue === mi.value}
            count={count("match_issue", mi.value)}
            title={mi.hint}
            onClick={() =>
              set({ match_issue: query.match_issue === mi.value ? undefined : mi.value, offset: 0 })
            }
          >
            {mi.label}
          </Chip>
        ))}
        <div className="mb-1 mt-2 text-[10px] uppercase tracking-wide text-ink-dim">状态</div>
        {MATCH_STATUSES.map((ms) => (
          <Chip
            key={ms.value}
            active={query.match_status === ms.value}
            count={count("match_status", ms.value)}
            title={ms.hint}
            onClick={() =>
              set({ match_status: query.match_status === ms.value ? undefined : ms.value, offset: 0 })
            }
          >
            {ms.label}
          </Chip>
        ))}
      </FilterGroup>

      <FilterGroup label="排序">
        {SORTS.map((s) => (
          <Chip
            key={s.value}
            active={query.sort === s.value}
            onClick={() => set({ sort: s.value })}
          >
            {s.label}
          </Chip>
        ))}
        <Chip active={!!query.desc} onClick={() => set({ desc: !query.desc })}>
          {query.desc ? "降序" : "升序"}
        </Chip>
      </FilterGroup>

      <div className="border-t border-border pt-3 text-xs text-ink-dim">
        共 {total} 部
      </div>
    </aside>
  );
}

function FilterGroup({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-ink-dim">
        {label}
      </div>
      <div className="flex flex-wrap gap-1.5">{children}</div>
    </div>
  );
}

function Chip({
  active,
  onClick,
  children,
  count,
  title,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
  count?: number;
  title?: string;
}) {
  return (
    <button onClick={onClick} title={title} className={cn("chip", active && "chip-active")}>
      {children}
      {count !== undefined && <span className="ml-1 text-ink-dim">({count})</span>}
    </button>
  );
}
