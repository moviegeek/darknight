import { useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, Eye, EyeOff, X } from "lucide-react";

import { addWatch, deleteWatch } from "../api/client";
import type { MovieDetail, WatchPlay } from "../api/types";
import { cn } from "../lib/format";

function todayDateInput(): string {
  const d = new Date();
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
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

function AddWatchDialog({
  busy,
  error,
  onCancel,
  onConfirm,
}: {
  busy: boolean;
  error?: string;
  onCancel: () => void;
  onConfirm: (watchedAt: number, hasTime: boolean) => void;
}) {
  const [date, setDate] = useState(todayDateInput);
  const [time, setTime] = useState("");
  const parsed = useMemo(() => toUnix(date, time), [date, time]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onCancel}
    >
      <div
        className="mx-4 w-full max-w-sm rounded-lg border border-border bg-bg-panel p-5 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 className="font-semibold text-ink">标记已看</h3>
        <p className="mt-1 text-sm text-ink-muted">选择观看日期；时间可选。</p>
        <div className="mt-4 space-y-3">
          <label className="block text-xs text-ink-dim">
            日期
            <input
              type="date"
              value={date}
              onChange={(e) => setDate(e.target.value)}
              required
              className="mt-1 w-full rounded-md border border-border bg-bg px-3 py-1.5 text-sm text-ink outline-none focus:border-ink-dim"
            />
          </label>
          <label className="block text-xs text-ink-dim">
            时间（可选）
            <input
              type="time"
              value={time}
              onChange={(e) => setTime(e.target.value)}
              className="mt-1 w-full rounded-md border border-border bg-bg px-3 py-1.5 text-sm text-ink outline-none focus:border-ink-dim"
            />
          </label>
        </div>
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
            disabled={busy || !parsed}
            onClick={() => parsed && onConfirm(parsed.watchedAt, parsed.hasTime)}
            className="inline-flex items-center gap-1.5 rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-white hover:bg-accent/90 disabled:opacity-40"
          >
            <Check className="h-3.5 w-3.5" />
            确认
          </button>
        </div>
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
