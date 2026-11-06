import { useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CalendarDays, Check, ChevronLeft, ChevronRight, Clock, Eye, EyeOff, Film, X } from "lucide-react";

import { addWatch, deleteWatch } from "../api/client";
import type { MovieDetail, WatchPlay } from "../api/types";
import { cn } from "../lib/format";

function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

function formatDateInput(d: Date): string {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
}

// Converts a date (YYYY-MM-DD) plus optional HH:MM into local unix seconds.
function toUnix(date: string, time: string): { watchedAt: number; hasTime: boolean } | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return null;
  const [y, mo, d] = date.split("-").map(Number);
  let hasTime = false;
  let h = 0;
  let mi = 0;
  if (time.trim()) {
    const m = time.trim().match(/^(\d{1,2}):(\d{2})$/);
    if (!m) return null;
    h = Number(m[1]);
    mi = Number(m[2]);
    if (h > 23 || mi > 59) return null;
    hasTime = true;
  }
  const dt = new Date(y, mo - 1, d, h, mi, 0, 0);
  if (Number.isNaN(dt.getTime())) return null;
  return { watchedAt: Math.floor(dt.getTime() / 1000), hasTime };
}

// Normalizes a TMDB-style release_date ("YYYY-MM-DD" or longer ISO) to YYYY-MM-DD.
function normalizeReleaseDate(raw: string | undefined): string | null {
  if (!raw) return null;
  const m = raw.trim().match(/^(\d{4}-\d{2}-\d{2})/);
  if (!m) return null;
  const [y, mo, d] = m[1].split("-").map(Number);
  const dt = new Date(y, mo - 1, d);
  if (dt.getFullYear() !== y || dt.getMonth() !== mo - 1 || dt.getDate() !== d) return null;
  return m[1];
}

function formatPlay(p: WatchPlay): string {
  if (!p.watched_at) return "日期未知";
  const d = new Date(p.watched_at * 1000);
  const date = d.toLocaleDateString();
  if (!p.has_time) return date;
  return `${date} ${d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}

export function WatchControls({ movie }: { movie: MovieDetail }) {
  const qc = useQueryClient();
  const [addOpen, setAddOpen] = useState(false);
  const [removeOpen, setRemoveOpen] = useState(false);

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["movie", movie.id] });
    qc.invalidateQueries({ queryKey: ["movies"] });
    qc.invalidateQueries({ queryKey: ["movie-facets"] });
  };

  const addMut = useMutation({
    mutationFn: ({ watchedAt, hasTime }: { watchedAt: number; hasTime: boolean }) =>
      addWatch(movie.id, watchedAt, hasTime),
    onSuccess: () => {
      setAddOpen(false);
      invalidate();
    },
  });

  const delMut = useMutation({
    mutationFn: (wid: number | "latest") => deleteWatch(movie.id, wid),
    onSuccess: () => {
      setRemoveOpen(false);
      invalidate();
    },
  });

  const watches = movie.watches ?? [];
  const watched = movie.watched || watches.length > 0;

  return (
    <div className="flex flex-wrap items-center gap-2">
      {watched && (
        <span className="text-xs text-ink-muted">
          已看 {movie.watch_count || watches.length} 次
          {watches[0]
            ? <> · 最近 {formatPlay(watches[0])}</>
            : movie.last_watched_at > 0
              ? <> · 最近 {new Date(movie.last_watched_at * 1000).toLocaleDateString()}</>
              : null}
        </span>
      )}
      <button
        type="button"
        onClick={() => setAddOpen(true)}
        className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs text-ink-muted hover:border-accent hover:text-accent"
      >
        <Eye className="h-3.5 w-3.5" />
        标记已看
      </button>
      {watched && (
        <button
          type="button"
          onClick={() => setRemoveOpen(true)}
          className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs text-ink-muted hover:border-rose-700 hover:text-rose-300"
        >
          <EyeOff className="h-3.5 w-3.5" />
          取消已看
        </button>
      )}

      {addOpen && (
        <AddWatchDialog
          releaseDate={movie.release_date}
          busy={addMut.isPending}
          error={addMut.error ? String(addMut.error) : undefined}
          onCancel={() => setAddOpen(false)}
          onConfirm={(watchedAt, hasTime) => addMut.mutate({ watchedAt, hasTime })}
        />
      )}
      {removeOpen && (
        <RemoveWatchDialog
          watches={watches}
          busy={delMut.isPending}
          error={delMut.error ? String(delMut.error) : undefined}
          onCancel={() => setRemoveOpen(false)}
          onConfirm={(wid) => delMut.mutate(wid)}
        />
      )}
    </div>
  );
}

type AddMode = "pick" | "other";

function AddWatchDialog({
  releaseDate,
  busy,
  error,
  onCancel,
  onConfirm,
}: {
  releaseDate?: string;
  busy: boolean;
  error?: string;
  onCancel: () => void;
  onConfirm: (watchedAt: number, hasTime: boolean) => void;
}) {
  const release = normalizeReleaseDate(releaseDate);
  const [mode, setMode] = useState<AddMode>("pick");
  const [date, setDate] = useState(() => formatDateInput(new Date()));
  const [time, setTime] = useState("");
  const parsed = useMemo(() => toUnix(date, time), [date, time]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const confirmRelease = () => {
    if (!release || busy) return;
    const p = toUnix(release, "");
    if (p) onConfirm(p.watchedAt, false);
  };

  const confirmNow = () => {
    if (busy) return;
    const now = new Date();
    onConfirm(Math.floor(now.getTime() / 1000), true);
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onCancel}
    >
      <div
        className="mx-4 w-full max-w-sm rounded-lg border border-border bg-bg-panel p-5 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-3">
          <div>
            <h3 className="font-semibold text-ink">标记已看</h3>
            <p className="mt-1 text-sm text-ink-muted">
              {mode === "pick" ? "选择何时观看了这部电影。" : "挑选日期；时间可选。"}
            </p>
          </div>
          <button
            type="button"
            onClick={onCancel}
            className="rounded p-1 text-ink-dim hover:bg-bg hover:text-ink"
            aria-label="关闭"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {mode === "pick" ? (
          <div className="mt-4 space-y-2">
            <QuickOption
              icon={<Film className="h-4 w-4" />}
              label="上映日观看"
              hint={release ? release : "无上映日期"}
              disabled={!release || busy}
              onClick={confirmRelease}
            />
            <QuickOption
              icon={<Clock className="h-4 w-4" />}
              label="刚刚"
              hint="现在的日期和时间"
              disabled={busy}
              onClick={confirmNow}
            />
            <QuickOption
              icon={<CalendarDays className="h-4 w-4" />}
              label="其他日期"
              hint="打开日历选择"
              disabled={busy}
              onClick={() => setMode("other")}
            />
            {error && <p className="pt-1 text-xs text-rose-300">{error}</p>}
          </div>
        ) : (
          <div className="mt-4 space-y-3">
            <MonthCalendar value={date} onChange={setDate} />
            <label className="block text-xs text-ink-dim">
              时间（可选）
              <input
                type="time"
                value={time}
                onChange={(e) => setTime(e.target.value)}
                className="mt-1 w-full rounded-md border border-border bg-bg px-3 py-1.5 text-sm text-ink outline-none focus:border-ink-dim"
              />
            </label>
            {error && <p className="text-xs text-rose-300">{error}</p>}
            <div className="flex justify-between gap-2 pt-1">
              <button
                type="button"
                onClick={() => setMode("pick")}
                className="rounded-md border border-border px-3 py-1.5 text-sm text-ink-muted hover:text-ink"
              >
                返回
              </button>
              <button
                type="button"
                disabled={busy || !parsed}
                onClick={() => parsed && onConfirm(parsed.watchedAt, parsed.hasTime)}
                className="inline-flex items-center gap-1.5 rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-white hover:bg-accent/90 disabled:opacity-40"
              >
                <Check className="h-3.5 w-3.5" />
                确认
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function QuickOption({
  icon,
  label,
  hint,
  disabled,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  hint: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-3 rounded-md border border-border px-3 py-2.5 text-left transition",
        disabled
          ? "cursor-not-allowed opacity-40"
          : "hover:border-accent hover:bg-accent/10"
      )}
    >
      <span className="text-accent">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="block text-sm font-medium text-ink">{label}</span>
        <span className="block truncate text-xs text-ink-dim">{hint}</span>
      </span>
    </button>
  );
}

const WEEKDAYS = ["一", "二", "三", "四", "五", "六", "日"] as const;

// MonthCalendar: compact month grid with year/month jump controls so older
// release years are reachable without dozens of arrow clicks.
function MonthCalendar({
  value,
  onChange,
}: {
  value: string; // YYYY-MM-DD
  onChange: (next: string) => void;
}) {
  const selected = useMemo(() => {
    const [y, m, d] = value.split("-").map(Number);
    return { y, m, d };
  }, [value]);
  const [viewYear, setViewYear] = useState(selected.y);
  const [viewMonth, setViewMonth] = useState(selected.m); // 1-12

  useEffect(() => {
    setViewYear(selected.y);
    setViewMonth(selected.m);
  }, [selected.y, selected.m]);

  const today = formatDateInput(new Date());
  const firstDow = new Date(viewYear, viewMonth - 1, 1).getDay(); // 0=Sun
  // Convert to Monday-first offset (0=Mon ... 6=Sun).
  const offset = (firstDow + 6) % 7;
  const daysInMonth = new Date(viewYear, viewMonth, 0).getDate();
  // Always fill 6 weeks (42 cells) so months with 5 vs 6 rows keep a stable
  // height and the prev/next arrows don't jump when navigating.
  const cells: Array<number | null> = [
    ...Array.from({ length: offset }, () => null),
    ...Array.from({ length: daysInMonth }, (_, i) => i + 1),
  ];
  while (cells.length < 42) cells.push(null);

  const shiftMonth = (delta: number) => {
    const d = new Date(viewYear, viewMonth - 1 + delta, 1);
    setViewYear(d.getFullYear());
    setViewMonth(d.getMonth() + 1);
  };

  const years = useMemo(() => {
    const nowY = new Date().getFullYear();
    const from = Math.min(selected.y, nowY) - 80;
    const to = Math.max(selected.y, nowY) + 5;
    const out: number[] = [];
    for (let y = to; y >= from; y--) out.push(y);
    return out;
  }, [selected.y]);

  return (
    <div className="rounded-md border border-border bg-bg p-3">
      <div className="mb-2 flex items-center gap-1">
        <button
          type="button"
          onClick={() => shiftMonth(-1)}
          className="rounded p-1 text-ink-dim hover:bg-bg-hover hover:text-ink"
          aria-label="上一月"
        >
          <ChevronLeft className="h-4 w-4" />
        </button>
        <select
          value={viewYear}
          onChange={(e) => setViewYear(Number(e.target.value))}
          className="min-w-0 flex-1 rounded-md border border-border bg-bg-panel px-2 py-1 text-sm text-ink outline-none"
          aria-label="年"
        >
          {years.map((y) => (
            <option key={y} value={y}>
              {y}年
            </option>
          ))}
        </select>
        <select
          value={viewMonth}
          onChange={(e) => setViewMonth(Number(e.target.value))}
          className="rounded-md border border-border bg-bg-panel px-2 py-1 text-sm text-ink outline-none"
          aria-label="月"
        >
          {Array.from({ length: 12 }, (_, i) => i + 1).map((m) => (
            <option key={m} value={m}>
              {m}月
            </option>
          ))}
        </select>
        <button
          type="button"
          onClick={() => shiftMonth(1)}
          className="rounded p-1 text-ink-dim hover:bg-bg-hover hover:text-ink"
          aria-label="下一月"
        >
          <ChevronRight className="h-4 w-4" />
        </button>
      </div>

      <div className="mb-1 grid grid-cols-7 gap-0.5 text-center text-[10px] text-ink-dim">
        {WEEKDAYS.map((w) => (
          <div key={w} className="py-1">
            {w}
          </div>
        ))}
      </div>
      <div className="grid h-48 grid-cols-7 grid-rows-6 gap-0.5">
        {cells.map((day, i) => {
          if (day == null) {
            return <div key={`e-${i}`} className="min-h-0" aria-hidden />;
          }
          const iso = `${viewYear}-${pad2(viewMonth)}-${pad2(day)}`;
          const isSelected = iso === value;
          const isToday = iso === today;
          return (
            <button
              key={iso}
              type="button"
              onClick={() => onChange(iso)}
              className={cn(
                "box-border flex min-h-0 items-center justify-center rounded-md border text-sm transition",
                isSelected
                  ? "border-accent bg-accent font-medium text-white"
                  : isToday
                    ? "border-accent/50 text-accent hover:bg-accent/10"
                    : "border-transparent text-ink-muted hover:bg-bg-hover hover:text-ink"
              )}
            >
              {day}
            </button>
          );
        })}
      </div>

      <div className="mt-2 flex items-center justify-between text-xs text-ink-dim">
        <span>已选 {value}</span>
        <button
          type="button"
          onClick={() => {
            const now = new Date();
            onChange(formatDateInput(now));
            setViewYear(now.getFullYear());
            setViewMonth(now.getMonth() + 1);
          }}
          className="text-accent hover:underline"
        >
          今天
        </button>
      </div>
    </div>
  );
}

function RemoveWatchDialog({
  watches,
  busy,
  error,
  onCancel,
  onConfirm,
}: {
  watches: WatchPlay[];
  busy: boolean;
  error?: string;
  onCancel: () => void;
  onConfirm: (wid: number | "latest") => void;
}) {
  // Default to the most recent play (first in the newest-first list).
  const defaultKey = watches[0] ? String(watches[0].id) : "latest";
  const [selected, setSelected] = useState(defaultKey);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const submit = () => {
    if (selected === "latest" || selected === "0") {
      onConfirm("latest");
      return;
    }
    onConfirm(Number(selected));
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onCancel}
    >
      <div
        className="mx-4 w-full max-w-sm rounded-lg border border-border bg-bg-panel p-5 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 className="font-semibold text-ink">取消已看</h3>
        <p className="mt-1 text-sm text-ink-muted">
          {watches.length > 1
            ? "选择要移除的一次观看记录（默认最近一次）。"
            : "将移除这条观看记录。"}
        </p>
        {watches.length > 0 ? (
          <div className="mt-4 max-h-56 space-y-1.5 overflow-y-auto">
            {watches.map((p, i) => {
              const key = String(p.id);
              return (
                <label
                  key={`${p.id}-${p.watched_at}-${i}`}
                  className={cn(
                    "flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm",
                    selected === key
                      ? "border-accent bg-accent/10 text-accent"
                      : "border-border text-ink-muted hover:border-ink-dim"
                  )}
                >
                  <input
                    type="radio"
                    name="watch-play"
                    className="accent-accent"
                    checked={selected === key}
                    onChange={() => setSelected(key)}
                  />
                  <span className="flex-1">{formatPlay(p)}</span>
                  {i === 0 && <span className="text-[10px] text-ink-dim">最近</span>}
                </label>
              );
            })}
          </div>
        ) : (
          <p className="mt-4 text-sm text-ink-dim">将清除已看状态。</p>
        )}
        {error && <p className="mt-3 text-xs text-rose-300">{error}</p>}
        <div className="mt-5 flex justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm text-ink-muted hover:text-ink"
          >
            <X className="h-3.5 w-3.5" />
            取消
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={submit}
            className="rounded-md bg-rose-800 px-4 py-1.5 text-sm font-medium text-rose-50 hover:bg-rose-700 disabled:opacity-40"
          >
            移除
          </button>
        </div>
      </div>
    </div>
  );
}
