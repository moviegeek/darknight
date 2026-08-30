import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FolderPlus, RefreshCw, Trash2, CheckCircle2 } from "lucide-react";

import {
  createLibrary,
  deleteLibrary,
  listLibraries,
  scanLibrary,
} from "../api/client";
import type { Library } from "../api/types";
import TraktSyncCard from "../components/TraktSyncCard";

export default function SettingsPage() {
  const qc = useQueryClient();
  const {
    data: libs = [],
    refetch,
    dataUpdatedAt,
  } = useQuery({
    queryKey: ["libraries"],
    queryFn: listLibraries,
  });

  const [name, setName] = useState("");
  const [path, setPath] = useState("");

  // Per-library scan state.
  // scanningId === null means no active scan; otherwise we are waiting for
  // library.id's last_scan_at to become greater than startedAt.
  const [scanningId, setScanningId] = useState<number | null>(null);
  const [startedAt, setStartedAt] = useState<number>(0);
  const [scanSuccessId, setScanSuccessId] = useState<number | null>(null);

  const addMut = useMutation({
    mutationFn: () => createLibrary(name, path),
    onSuccess: () => {
      setName("");
      setPath("");
      qc.invalidateQueries({ queryKey: ["libraries"] });
    },
  });

  const scanMut = useMutation({
    mutationFn: (lib: Library) => scanLibrary(lib.id),
    onMutate: (lib) => {
      setScanningId(lib.id);
      setStartedAt(lib.last_scan_at);
      setScanSuccessId(null);
    },
  });

  const delMut = useMutation({
    mutationFn: (id: number) => deleteLibrary(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["libraries"] }),
  });

  // Poll library list while a scan is in progress. `listLibraries` already
  // refetches on window focus / network reconnect; here we also force a refresh
  // every 2 seconds until the target library's last_scan_at changes.
  useEffect(() => {
    if (scanningId == null) return;

    const target = libs.find((l) => l.id === scanningId);
    if (!target) return;

    if (target.last_scan_at > startedAt) {
      // scan completed
      setScanSuccessId(scanningId);
      setScanningId(null);
      qc.invalidateQueries({ queryKey: ["movies"] });
      return;
    }

    const timer = setInterval(() => {
      refetch();
    }, 2000);

    return () => clearInterval(timer);
  }, [scanningId, startedAt, libs, dataUpdatedAt, qc, refetch]);

  // Clear the "scan complete" message after 5 seconds.
  useEffect(() => {
    if (scanSuccessId == null) return;
    const timer = setTimeout(() => setScanSuccessId(null), 5000);
    return () => clearTimeout(timer);
  }, [scanSuccessId]);

  return (
    <div className="mx-auto max-w-3xl px-6 py-8">
      <h1 className="text-2xl font-bold text-ink">媒体库设置</h1>

      <section className="mt-6 rounded-lg border border-border bg-bg-panel p-5">
        <h2 className="mb-3 flex items-center gap-2 font-semibold text-ink">
          <FolderPlus className="h-4 w-4" /> 添加媒体库
        </h2>
        <div className="flex flex-col gap-3 sm:flex-row">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="名称（如：电影）"
            className="flex-1 rounded-md border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-ink-dim"
          />
          <input
            value={path}
            onChange={(e) => setPath(e.target.value)}
            placeholder="根目录绝对路径（如：/Volumes/Media/Films）"
            className="flex-[2] rounded-md border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-ink-dim"
          />
          <button
            disabled={!name || !path || addMut.isPending}
            onClick={() => addMut.mutate()}
            className="rounded-md bg-accent px-4 py-2 text-sm font-medium text-white disabled:opacity-40"
          >
            {addMut.isPending ? "添加中…" : "添加"}
          </button>
        </div>
        {addMut.isError && (
          <p className="mt-2 text-sm text-accent">{String(addMut.error)}</p>
        )}
      </section>

      <section className="mt-6 rounded-lg border border-border bg-bg-panel p-5">
        <h2 className="mb-3 font-semibold text-ink">已配置的媒体库</h2>
        {libs.length === 0 ? (
          <p className="text-sm text-ink-muted">还没有添加任何媒体库。</p>
        ) : (
          <ul className="divide-y divide-border">
            {libs.map((lib) => {
              const isScanning = scanningId === lib.id;
              const justFinished = scanSuccessId === lib.id;
              return (
                <li key={lib.id} className="flex items-center gap-3 py-3">
                  <div className="flex-1">
                    <div className="font-medium text-ink">{lib.name}</div>
                    <div className="text-xs text-ink-muted">{lib.root_path}</div>
                    <div className="mt-1 text-xs text-ink-dim">
                      {lib.last_scan_at
                        ? `上次扫描：${new Date(lib.last_scan_at * 1000).toLocaleString()}`
                        : "从未扫描"}
                    </div>
                  </div>
                  <button
                    onClick={() => scanMut.mutate(lib)}
                    disabled={scanningId != null}
                    className="inline-flex items-center gap-1 rounded-md border border-border px-3 py-1.5 text-sm text-ink-muted hover:text-ink disabled:opacity-40"
                  >
                    <RefreshCw
                      className={isScanning ? "h-4 w-4 animate-spin" : "h-4 w-4"}
                    />
                    {isScanning ? "扫描中…" : "扫描"}
                  </button>
                  <button
                    onClick={() => delMut.mutate(lib.id)}
                    className="rounded-md border border-border p-1.5 text-ink-dim hover:text-accent"
                    title="删除"
                  >
                    <Trash2 className="h-4 w-4" />
                  </button>
                  {justFinished && (
                    <span className="inline-flex items-center gap-1 text-xs text-emerald-400">
                      <CheckCircle2 className="h-4 w-4" />
                      扫描完成
                    </span>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <TraktSyncCard />

      <p className="mt-6 text-xs text-ink-dim">
        提示：扫描需要系统已安装 ffmpeg/ffprobe，用于读取文件的真实编码、码率、音轨等信息。
        <button onClick={() => refetch()} className="ml-2 underline">
          刷新
        </button>
      </p>
    </div>
  );
}
