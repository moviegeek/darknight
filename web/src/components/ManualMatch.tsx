import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Check, FolderPen, Loader2, Search, Wand2, X } from "lucide-react";

import {
  matchMovie,
  renameMovieFile,
  searchCandidates,
  tmdbImage,
  unmatchMovie,
} from "../api/client";
import type { MatchCandidate, MovieDetail, MovieFile, RenamePlan } from "../api/types";
import { cn } from "../lib/format";

// ManualMatchButton opens the manual-match panel. It is the entry point for
// fixing a release the automatic matcher could not resolve (or resolved
// wrongly): search TMDB by hand, confirm a candidate, then optionally rename
// the files on disk to the matched title.
export function ManualMatchButton({
  movie,
  files,
}: {
  movie: MovieDetail;
  files: MovieFile[];
}) {
  const [open, setOpen] = useState(false);
  const needsAttention = !movie.tmdb_id;

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className={cn(
          "inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm transition",
          needsAttention
            ? "border-rose-700 bg-rose-950/40 text-rose-200 hover:bg-rose-900/40"
            : "border-border text-ink-muted hover:text-ink"
        )}
        title={needsAttention ? "此电影缺少 TMDB 信息，点击手动匹配" : "重新匹配 TMDB"}
      >
        <Wand2 className="h-4 w-4" />
        {needsAttention ? "手动匹配" : "重新匹配"}
      </button>
      {open && <ManualMatchDialog movie={movie} files={files} onClose={() => setOpen(false)} />}
    </>
  );
}

function ManualMatchDialog({
  movie,
  files,
  onClose,
}: {
  movie: MovieDetail;
  files: MovieFile[];
  onClose: () => void;
}) {
  const qc = useQueryClient();
  // seed the search box with the parsed title and year; the user can refine
  // either. The year is optional - clearing it searches year-agnostic.
  const [term, setTerm] = useState(movie.title);
  const [year, setYear] = useState(movie.year > 0 ? String(movie.year) : "");
  const [query, setQuery] = useState(movie.title);
  const [queryYear, setQueryYear] = useState(movie.year || undefined);
  const [matched, setMatched] = useState<MatchCandidate | null>(null);

  const { data, isFetching, error } = useQuery({
    queryKey: ["candidates", movie.id, query, queryYear],
    queryFn: () => searchCandidates(query, movie.id, queryYear),
    enabled: query.trim().length > 0,
  });

  const confirm = useMutation({
    mutationFn: (tmdbId: number) => matchMovie(movie.id, tmdbId),
    onSuccess: async (res, tmdbId) => {
      await qc.invalidateQueries({ queryKey: ["movie", movie.id] });
      await qc.invalidateQueries({ queryKey: ["movie-files", movie.id] });
      await qc.invalidateQueries({ queryKey: ["movie-cast", movie.id] });
      const cand = (data?.candidates ?? []).find((c) => c.tmdb_id === tmdbId) ?? null;
      // a merge means this row is gone; send the user to the surviving movie
      if (res.merged_into) {
        window.location.href = `/movie/${res.merged_into}`;
        return;
      }
      setMatched(cand);
    },
  });

  const detach = useMutation({
    mutationFn: () => unmatchMovie(movie.id),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["movie", movie.id] });
      onClose();
    },
  });

  // once matched, offer the on-disk rename
  if (matched) {
    return (
      <RenameDialog
        movie={movie}
        files={files}
        matchedTitle={matched.title}
        onClose={onClose}
      />
    );
  }

  return (
    <Modal onClose={onClose} title="手动匹配 TMDB">
      <div className="space-y-4">
        <div className="rounded-md bg-bg-panel p-3 text-xs text-ink-muted">
          当前条目：
          <span className="ml-1 font-medium text-ink">{movie.title}</span>
          {movie.year ? <span className="ml-1">({movie.year})</span> : null}
          {!movie.tmdb_id && (
            <span className="ml-2 inline-flex items-center gap-1 text-rose-300">
              <AlertTriangle className="h-3 w-3" /> 缺少 TMDB
            </span>
          )}
        </div>

        <form
          onSubmit={(e) => {
            e.preventDefault();
            setQuery(term);
            setQueryYear(Number(year) || undefined);
          }}
          className="flex gap-2"
        >
          <div className="relative flex-1">
            <Search className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-ink-dim" />
            <input
              autoFocus
              value={term}
              onChange={(e) => setTerm(e.target.value)}
              placeholder="输入电影标题搜索 TMDB…"
              className="w-full rounded-md border border-border bg-bg py-2 pl-8 pr-3 text-sm outline-none focus:border-ink-dim"
            />
          </div>
          <input
            value={year}
            onChange={(e) => setYear(e.target.value.replace(/\D/g, "").slice(0, 4))}
            placeholder="年份"
            title="可选：影响候选排序；清空则不使用年份"
            inputMode="numeric"
            className="w-20 rounded-md border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-ink-dim"
          />
          <button
            type="submit"
            className="rounded-md border border-border px-3 py-2 text-sm text-ink-muted hover:text-ink"
          >
            搜索
          </button>
        </form>

        {isFetching && (
          <div className="flex items-center gap-2 py-6 text-sm text-ink-dim">
            <Loader2 className="h-4 w-4 animate-spin" /> 搜索中…
          </div>
        )}
        {error && (
          <p className="py-4 text-sm text-rose-300">搜索失败：{(error as Error).message}</p>
        )}
        {!isFetching && data && data.candidates.length === 0 && (
          <p className="py-6 text-sm text-ink-muted">没有找到候选，试试换个标题（原名/英文名）。</p>
        )}

        {!isFetching && data && data.candidates.length > 0 && (
          <ul className="max-h-[42vh] space-y-2 overflow-y-auto">
            {data.candidates.map((c) => (
              <li key={c.tmdb_id}>
                <button
                  disabled={confirm.isPending}
                  onClick={() => confirm.mutate(c.tmdb_id)}
                  className="flex w-full items-start gap-3 rounded-md border border-border p-2 text-left transition hover:border-ink-dim hover:bg-bg-card disabled:opacity-50"
                >
                  <div className="h-[72px] w-12 shrink-0 overflow-hidden rounded bg-bg">
                    {tmdbImage(c.poster_path, "w185") ? (
                      <img
                        src={tmdbImage(c.poster_path, "w185")}
                        alt=""
                        className="h-full w-full object-cover"
                      />
                    ) : null}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate text-sm font-medium text-ink">{c.title}</span>
                      {c.year > 0 && <span className="text-xs text-ink-muted">({c.year})</span>}
                      <ScoreBadge score={c.score} />
                    </div>
                    {c.original_title && c.original_title !== c.title && (
                      <div className="truncate text-xs text-ink-dim">{c.original_title}</div>
                    )}
                    {c.overview && (
                      <p className="mt-1 line-clamp-2 text-xs text-ink-muted">{c.overview}</p>
                    )}
                  </div>
                </button>
              </li>
            ))}
          </ul>
        )}

        {movie.tmdb_id > 0 && (
          <div className="border-t border-border pt-3">
            <button
              onClick={() => detach.mutate()}
              disabled={detach.isPending}
              className="text-xs text-ink-dim hover:text-rose-300"
            >
              取消当前匹配（回到未匹配状态）
            </button>
          </div>
        )}
      </div>
    </Modal>
  );
}

// RenameDialog offers the optional on-disk rename after a successful match:
// the release dir, the video file, subtitles and .nfo are renamed to follow
// the library's convention. The plan is computed server-side (dry run) so what
// the user sees is exactly what will happen.
function RenameDialog({
  movie,
  files,
  matchedTitle,
  onClose,
}: {
  movie: MovieDetail;
  files: MovieFile[];
  matchedTitle: string;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const renamable = files.filter((f) => !f.is_disc);

  const previews = useQuery({
    queryKey: ["rename-preview", movie.id, renamable.map((f) => f.id).join(",")],
    queryFn: async () => {
      const out: Array<{ file: MovieFile; plan: RenamePlan }> = [];
      for (const f of renamable) {
        const res = await renameMovieFile(movie.id, f.id, true);
        if (res.changed) out.push({ file: f, plan: res.plan });
      }
      return out;
    },
    enabled: renamable.length > 0,
  });

  const apply = useMutation({
    mutationFn: async () => {
      for (const p of previews.data ?? []) {
        await renameMovieFile(movie.id, p.file.id, false);
      }
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["movie-files", movie.id] });
      onClose();
    },
  });

  const plans = previews.data ?? [];

  return (
    <Modal onClose={onClose} title="匹配成功">
      <div className="space-y-4">
        <div className="flex items-start gap-2 rounded-md border border-emerald-800 bg-emerald-950/30 p-3 text-sm">
          <Check className="mt-0.5 h-4 w-4 shrink-0 text-emerald-400" />
          <div>
            已匹配到 <span className="font-medium text-ink">{matchedTitle}</span>
            ，元数据（封面、简介、演职员）已更新。
          </div>
        </div>

        {previews.isFetching && (
          <div className="flex items-center gap-2 py-4 text-sm text-ink-dim">
            <Loader2 className="h-4 w-4 animate-spin" /> 计算重命名方案…
          </div>
        )}

        {!previews.isFetching && plans.length === 0 && (
          <p className="text-sm text-ink-muted">
            磁盘文件名已符合命名规范，无需重命名。
          </p>
        )}

        {plans.length > 0 && (
          <>
            <div className="flex items-center gap-1.5 text-sm text-ink">
              <FolderPen className="h-4 w-4 text-amber-400" />
              是否将磁盘上的文件重命名为匹配到的电影名？
            </div>
            {plans.length > 1 && (
              <div className="flex items-start gap-1.5 rounded-md border border-amber-800 bg-amber-950/30 px-2.5 py-2 text-xs text-amber-200">
                <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                这部电影有 {plans.length} 个版本（可能分布在不同资料库），点击后会全部重命名。
              </div>
            )}
            <div className="max-h-[38vh] space-y-3 overflow-y-auto">
              {plans.map((p) => (
                <div key={p.file.id} className="rounded-md border border-border p-2.5">
                  <div className="mb-1.5 flex items-center gap-1.5 text-xs">
                    <span className="badge bg-bg-card text-ink-muted">{p.file.library_name}</span>
                    <span className="text-ink-dim">
                      {p.file.resolution || "?"} · {p.file.source || "?"}
                    </span>
                  </div>
                  <DiffLine label="目录" from={p.plan.dir_old} to={p.plan.dir_new} />
                  {p.plan.moves.map((mv) => (
                    <DiffLine
                      key={mv.from}
                      label={kindLabel(mv.kind)}
                      from={baseName(mv.from)}
                      to={baseName(mv.to)}
                    />
                  ))}
                </div>
              ))}
            </div>
            {apply.error && (
              <p className="text-sm text-rose-300">重命名失败：{(apply.error as Error).message}</p>
            )}
            <div className="flex justify-end gap-2 border-t border-border pt-3">
              <button
                onClick={onClose}
                className="rounded-md border border-border px-3 py-1.5 text-sm text-ink-muted hover:text-ink"
              >
                暂不重命名
              </button>
              <button
                onClick={() => apply.mutate()}
                disabled={apply.isPending}
                className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-bg disabled:opacity-60"
              >
                {apply.isPending && <Loader2 className="h-4 w-4 animate-spin" />}
                重命名文件
              </button>
            </div>
          </>
        )}

        {plans.length === 0 && !previews.isFetching && (
          <div className="flex justify-end border-t border-border pt-3">
            <button
              onClick={onClose}
              className="rounded-md border border-border px-3 py-1.5 text-sm text-ink-muted hover:text-ink"
            >
              完成
            </button>
          </div>
        )}
      </div>
    </Modal>
  );
}

function DiffLine({ label, from, to }: { label: string; from: string; to: string }) {
  if (from === to) return null;
  return (
    <div className="mb-1.5 last:mb-0">
      <div className="text-[10px] uppercase tracking-wide text-ink-dim">{label}</div>
      <div className="break-all font-mono text-xs text-rose-300/80 line-through">{from}</div>
      <div className="break-all font-mono text-xs text-emerald-300">{to}</div>
    </div>
  );
}

function ScoreBadge({ score }: { score: number }) {
  const tone =
    score >= 85 ? "bg-emerald-900/60 text-emerald-200" : score >= 60 ? "bg-amber-900/60 text-amber-200" : "bg-bg-card text-ink-dim";
  return <span className={cn("badge", tone)}>{Math.round(score)}</span>;
}

function kindLabel(kind: string): string {
  switch (kind) {
    case "video":
      return "视频";
    case "subtitle":
      return "字幕";
    case "nfo":
      return "NFO";
    default:
      return "文件";
  }
}

function baseName(p: string): string {
  return p.split("/").pop() || p;
}

function Modal({
  title,
  children,
  onClose,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/60 p-6"
      onClick={onClose}
    >
      <div
        className="mt-12 w-full max-w-2xl rounded-lg border border-border bg-bg-panel p-5 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center justify-between">
          <h3 className="text-base font-semibold text-ink">{title}</h3>
          <button onClick={onClose} className="text-ink-dim hover:text-ink">
            <X className="h-4 w-4" />
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
