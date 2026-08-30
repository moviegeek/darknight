import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ChevronDown, ChevronRight, FileWarning, ShieldCheck, Trash2 } from "lucide-react";
import { deleteMovie, listMovies } from "../api/client";
import { cn } from "../lib/format";
import { ConfirmDialog } from "./ConfirmDialog";
import type { MovieListItem } from "../api/types";

// The diagnostic SQL dropped into the console editor by the toolbar button.
// It mirrors the panel's data source so the rows can be inspected - or, in
// write mode, cleaned up - with the full SQL toolset.
const DIAGNOSTIC_SQL = `-- 无文件电影：数据库里有 movies 行，但没有任何 movie_files 关联
SELECT m.id, m.title, m.year, m.tmdb_id, m.match_status, m.fail_reason,
       datetime(m.updated_at, 'unixepoch') AS updated
FROM movies m
WHERE NOT EXISTS (SELECT 1 FROM movie_files mf WHERE mf.movie_id = m.id)
ORDER BY m.updated_at DESC;`;

const MATCH_STATUS_LABELS: Record<string, string> = {
  matched: "已匹配",
  pending: "待审核",
  unmatched: "未匹配",
  manual: "手动",
};

// NoFileMoviesPanel is the console's data-health view for orphaned movie rows.
// When a release disappears from disk the scanner prunes its movie_files row
// but keeps the movies row as a metadata cache; the library hides such rows
// (they are index entries, not content), so this panel is the only place they
// surface - for debugging and manual cleanup (each row can be deleted).
export function NoFileMoviesPanel({ onLoadSql }: { onLoadSql: (sql: string) => void }) {
  const [open, setOpen] = useState(true);
  const [pendingDelete, setPendingDelete] = useState<MovieListItem | null>(null);
  const qc = useQueryClient();

  const { data, isLoading } = useQuery({
    queryKey: ["no-file-movies"],
    queryFn: () => listMovies({ match_issue: "no_files", sort: "added", desc: true, limit: 0 }),
  });
  const items = data?.items ?? [];
  const healthy = !isLoading && items.length === 0;

  const delMut = useMutation({
    mutationFn: (id: number) => deleteMovie(id),
    onSuccess: () => {
      setPendingDelete(null);
      // refresh this panel plus the library list/facets the row counts feed
      qc.invalidateQueries({ queryKey: ["no-file-movies"] });
      qc.invalidateQueries({ queryKey: ["movies"] });
      qc.invalidateQueries({ queryKey: ["movie-facets"] });
    },
  });

  return (
    <section className="mb-4 rounded-lg border border-border bg-bg-panel">
      {/* header: toggle + title + count + diagnostic-SQL action */}
      <div className="flex items-center gap-2 px-4 py-2.5">
        <button
          onClick={() => setOpen(!open)}
          className="text-ink-dim hover:text-ink"
          title={open ? "收起" : "展开"}
        >
          {open ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
        </button>
        {healthy ? (
          <ShieldCheck className="h-4 w-4 shrink-0 text-emerald-400" />
        ) : (
          <FileWarning className="h-4 w-4 shrink-0 text-accent" />
        )}
        <h2 className="text-sm font-semibold text-ink">无文件电影</h2>
        {!isLoading && (
          <span className={cn("badge", healthy ? "badge-disc" : "badge-warn")}>{items.length}</span>
        )}
        <p className="ml-2 hidden flex-1 truncate text-xs text-ink-dim md:block">
          有电影条目但没有关联的磁盘文件（文件被移动或删除后遗留），已从资料库隐藏，仅在此处显示。
        </p>
        <button
          onClick={() => onLoadSql(DIAGNOSTIC_SQL)}
          className="ml-auto shrink-0 rounded-md border border-border px-2.5 py-1 text-xs text-ink-muted hover:text-ink"
          title="把诊断查询填入下方 SQL 编辑器"
        >
          载入诊断 SQL
        </button>
      </div>

      {open && (
        <div className="border-t border-border">
          {isLoading ? (
            <p className="px-4 py-3 text-xs text-ink-dim">加载中…</p>
          ) : items.length === 0 ? (
            <p className="px-4 py-3 text-xs text-ink-muted">
              没有无文件的电影，数据库中的电影条目都有对应的磁盘文件。
            </p>
          ) : (
            <>
              {delMut.isError && (
                <p className="border-b border-border px-4 py-2 text-xs text-accent">
                  删除失败：{String(delMut.error)}
                </p>
              )}
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead className="text-left text-xs uppercase tracking-wide text-ink-dim">
                    <tr>
                      <th className="px-4 py-2 font-semibold">ID</th>
                      <th className="px-3 py-2 font-semibold">标题</th>
                      <th className="px-3 py-2 font-semibold">年份</th>
                      <th className="px-3 py-2 font-semibold">TMDB</th>
                      <th className="px-3 py-2 font-semibold">匹配状态</th>
                      <th className="px-3 py-2 font-semibold">更新时间</th>
                      <th className="px-3 py-2 font-semibold">操作</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {items.map((m) => (
                      <tr key={m.id} className="text-ink-muted hover:bg-bg-card">
                        <td className="px-4 py-1.5 font-mono text-xs">{m.id}</td>
                        <td className="max-w-64 truncate px-3 py-1.5 text-ink" title={m.title}>
                          {m.title}
                        </td>
                        <td className="px-3 py-1.5 text-xs">{m.year || "-"}</td>
                        <td className="px-3 py-1.5 font-mono text-xs">
                          {m.tmdb_id || <span className="text-ink-dim italic">无</span>}
                        </td>
                        <td className="px-3 py-1.5 text-xs" title={m.fail_reason || undefined}>
                          {MATCH_STATUS_LABELS[m.match_status] ?? m.match_status}
                        </td>
                        <td className="whitespace-nowrap px-3 py-1.5 text-xs">
                          {new Date(m.updated_at * 1000).toLocaleString()}
                        </td>
                        <td className="whitespace-nowrap px-3 py-1.5 text-xs">
                          <Link to={`/movie/${m.id}`} className="text-accent hover:underline">
                            详情
                          </Link>
                          <button
                            onClick={() => setPendingDelete(m)}
                            disabled={delMut.isPending}
                            className="ml-3 inline-flex items-center gap-1 rounded-md border border-border px-2 py-0.5 text-ink-muted hover:border-accent hover:text-accent disabled:opacity-40"
                            title="删除这个数据库条目"
                          >
                            <Trash2 className="h-3 w-3" />
                            删除
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
        </div>
      )}

      {pendingDelete && (
        <ConfirmDialog
          title="删除电影条目？"
          message={`将删除《${pendingDelete.title}》（id ${pendingDelete.id}）的数据库条目。该条目没有关联的磁盘文件，删除不影响磁盘上的任何文件。`}
          confirmLabel="删除"
          cancelLabel="取消"
          onConfirm={() => delMut.mutate(pendingDelete.id)}
          onCancel={() => {
            if (!delMut.isPending) setPendingDelete(null);
          }}
        />
      )}
    </section>
  );
}
