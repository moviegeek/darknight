import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Clock,
  Eraser,
  History,
  Play,
} from "lucide-react";
import { execSQL, listTables } from "../api/client";
import type { SqlResult } from "../api/types";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { NoFileMoviesPanel } from "../components/NoFileMoviesPanel";
import { useSqlHistory } from "../lib/useSqlHistory";
import { cn } from "../lib/format";

export default function SqlConsolePage() {
  const [writeMode, setWriteMode] = useState(false);
  const [pendingWriteSwitch, setPendingWriteSwitch] = useState(false);
  const [sql, setSql] = useState("");
  const [result, setResult] = useState<SqlResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [showHistory, setShowHistory] = useState(false);
  const [history, pushHistory] = useSqlHistory();

  const { data: tables } = useQuery({ queryKey: ["dev-tables"], queryFn: listTables });

  const mut = useMutation({
    mutationFn: () => execSQL(sql, writeMode),
    onSuccess: (r) => {
      setResult(r);
      setError(null);
      pushHistory(sql);
    },
    onError: (e: Error) => {
      setError(String(e));
      setResult(null);
      pushHistory(sql);
    },
  });

  const run = () => {
    if (!sql.trim()) return;
    mut.mutate();
  };

  // Ctrl/Cmd+Enter runs the query.
  const onKeyDown = (e: React.KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      run();
    }
  };

  // Clicking a table name loads a quick SELECT.
  const loadTableSelect = (name: string) => {
    setSql(`SELECT * FROM ${name} LIMIT 100;`);
  };

  return (
    <div className="mx-auto max-w-[1600px] px-6 py-6">
      {/* title bar + mode toggle */}
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold text-ink">SQL 控制台</h1>
        <ModeToggle
          writeMode={writeMode}
          onSwitchToWrite={() => setPendingWriteSwitch(true)}
          onSwitchToRead={() => setWriteMode(false)}
        />
      </div>

      {/* write-mode warning banner */}
      {writeMode && (
        <div className="mb-4 flex items-center gap-2 rounded-md border border-accent/40 bg-accent/10 px-4 py-2.5 text-sm text-accent">
          <AlertTriangle className="h-4 w-4 shrink-0" />
          <span>
            写入模式已开启 — 您的 SQL 语句可以修改数据库。请谨慎操作。
          </span>
        </div>
      )}

      {/* data-health: orphaned movie rows hidden from the library */}
      <NoFileMoviesPanel onLoadSql={setSql} />

      <div className="flex gap-5">
        {/* left: tables panel */}
        <aside className="w-64 shrink-0 space-y-1 overflow-y-auto rounded-lg border border-border bg-bg-panel px-3 py-3" style={{ maxHeight: "calc(100vh - 220px)" }}>
          <div className="mb-2 px-1 text-xs font-semibold uppercase tracking-wide text-ink-dim">
            数据表
          </div>
          {tables?.map((t) => (
            <div key={t.name}>
              <div className="flex items-center">
                <button
                  onClick={() => setExpanded(expanded === t.name ? null : t.name)}
                  className="flex items-center gap-0.5 py-0.5 text-ink-dim hover:text-ink"
                >
                  {expanded === t.name ? (
                    <ChevronDown className="h-3.5 w-3.5" />
                  ) : (
                    <ChevronRight className="h-3.5 w-3.5" />
                  )}
                </button>
                <button
                  onClick={() => loadTableSelect(t.name)}
                  className="flex-1 truncate rounded px-1 py-0.5 text-left font-mono text-xs text-ink-muted hover:bg-bg-card hover:text-ink"
                  title={`SELECT * FROM ${t.name}`}
                >
                  {t.name}
                </button>
              </div>
              {expanded === t.name && (
                <div className="ml-5 mt-0.5 space-y-0.5 border-l border-border pl-2">
                  {t.columns.map((c) => (
                    <div key={c.cid} className="flex items-center gap-1.5 py-0.5 text-xs">
                      {c.pk > 0 && (
                        <span className="badge badge-disc">PK</span>
                      )}
                      <span className="font-mono text-ink-muted">{c.name}</span>
                      <span className="text-ink-dim">{c.type || "any"}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
          {tables && tables.length === 0 && (
            <p className="px-1 py-2 text-xs text-ink-dim">没有数据表</p>
          )}
        </aside>

        {/* right: editor + results */}
        <div className="min-w-0 flex-1 space-y-4">
          {/* editor toolbar */}
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <button
                onClick={run}
                disabled={!sql.trim() || mut.isPending}
                className="inline-flex items-center gap-1.5 rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-white disabled:opacity-40"
              >
                <Play className="h-3.5 w-3.5" />
                {mut.isPending ? "执行中…" : "执行"}
              </button>
              <button
                onClick={() => { setSql(""); setResult(null); setError(null); }}
                className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs text-ink-muted hover:text-ink"
              >
                <Eraser className="h-3.5 w-3.5" />
                清除
              </button>
            </div>
            <button
              onClick={() => setShowHistory(!showHistory)}
              className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs text-ink-muted hover:text-ink"
            >
              <History className="h-3.5 w-3.5" />
              历史 ({history.length})
            </button>
          </div>

          {/* history dropdown */}
          {showHistory && history.length > 0 && (
            <div className="rounded-md border border-border bg-bg-panel p-2">
              {history.map((h, i) => (
                <button
                  key={i}
                  onClick={() => { setSql(h); setShowHistory(false); }}
                  className="block w-full truncate rounded px-2 py-1 text-left font-mono text-xs text-ink-muted hover:bg-bg-card hover:text-ink"
                  title={h}
                >
                  {h}
                </button>
              ))}
            </div>
          )}

          {/* SQL editor */}
          <textarea
            value={sql}
            onChange={(e) => setSql(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="输入 SQL… (Ctrl/Cmd + Enter 执行)&#10;例如: SELECT * FROM movies LIMIT 10;"
            spellCheck={false}
            className="h-40 w-full resize-y rounded-md border border-border bg-bg px-3 py-2.5 font-mono text-sm text-ink outline-none focus:border-ink-dim"
          />

          {/* error banner */}
          {error && (
            <div className="rounded-md border border-accent/40 bg-accent/10 px-4 py-3 text-sm text-accent">
              {error}
            </div>
          )}

          {/* results */}
          {result && <ResultPanel result={result} />}
        </div>
      </div>

      {/* write-mode confirmation dialog */}
      {pendingWriteSwitch && (
        <ConfirmDialog
          title="切换到写入模式？"
          message="写入模式允许执行 INSERT、UPDATE、DELETE、DROP 等语句，可能修改或删除数据库中的数据。请确认您了解风险。"
          confirmLabel="确认切换"
          cancelLabel="取消"
          onConfirm={() => { setWriteMode(true); setPendingWriteSwitch(false); }}
          onCancel={() => setPendingWriteSwitch(false)}
        />
      )}
    </div>
  );
}

// ModeToggle is the read-only / write switch. Switching to write requires
// confirmation (handled by the parent); switching back to read is immediate.
function ModeToggle({
  writeMode,
  onSwitchToWrite,
  onSwitchToRead,
}: {
  writeMode: boolean;
  onSwitchToWrite: () => void;
  onSwitchToRead: () => void;
}) {
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className={!writeMode ? "font-medium text-ink" : "text-ink-dim"}>只读</span>
      <button
        onClick={() => (writeMode ? onSwitchToRead() : onSwitchToWrite())}
        className={cn(
          "relative h-5 w-10 rounded-full transition-colors",
          writeMode ? "bg-accent" : "bg-border"
        )}
        role="switch"
        aria-checked={writeMode}
      >
        <span
          className={cn(
            "absolute top-0.5 h-4 w-4 rounded-full bg-white transition-transform",
            writeMode ? "translate-x-5" : "translate-x-0.5"
          )}
        />
      </button>
      <span className={writeMode ? "font-medium text-accent" : "text-ink-dim"}>写入</span>
    </div>
  );
}

// ResultPanel renders either a result table (for queries) or a summary line
// (for exec statements).
function ResultPanel({ result }: { result: SqlResult }) {
  const hasRows = result.columns.length > 0;

  if (!hasRows) {
    return (
      <div className="space-y-2">
        <div className="rounded-md border border-border bg-bg-panel px-4 py-3 text-sm text-ink-muted">
          执行成功 · 影响行数 <span className="font-medium text-ink">{result.rows_affected}</span>
          {result.last_insert_id ? <> · 最后插入 ID <span className="font-medium text-ink">{result.last_insert_id}</span></> : null}
        </div>
        <DurationFooter ms={result.duration_ms} rowCount={0} />
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <div className="overflow-x-auto rounded-lg border border-border">
        <table className="w-full text-sm">
          <thead className="bg-bg-panel text-left text-xs uppercase tracking-wide text-ink-dim">
            <tr>
              {result.columns.map((c) => (
                <th key={c} className="whitespace-nowrap px-3 py-2 font-semibold">{c}</th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {result.rows.map((row, i) => (
              <tr key={i} className="text-ink-muted hover:bg-bg-card">
                {row.map((cell, j) => (
                  <td key={j} className="whitespace-nowrap px-3 py-1.5 font-mono text-xs">
                    {cell === null || cell === undefined ? (
                      <span className="text-ink-dim italic">NULL</span>
                    ) : (
                      String(cell)
                    )}
                  </td>
                ))}
              </tr>
            ))}
            {result.rows.length === 0 && (
              <tr>
                <td colSpan={result.columns.length} className="px-3 py-6 text-center text-ink-dim">
                  无数据
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <DurationFooter ms={result.duration_ms} rowCount={result.rows.length} />
    </div>
  );
}

function DurationFooter({ ms, rowCount }: { ms: number; rowCount: number }) {
  return (
    <div className="flex items-center gap-1.5 px-1 text-xs text-ink-dim">
      <Clock className="h-3 w-3" />
      {rowCount} 行 · {ms} ms
    </div>
  );
}
