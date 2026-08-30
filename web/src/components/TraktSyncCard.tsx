// Settings-page card for the trakt.tv watch-status sync: device-flow connect
// (code + activation link), sync-now with result summary, disconnect.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Link2, RefreshCw, Unlink } from "lucide-react";

import {
  connectTrakt,
  disconnectTrakt,
  getTraktStatus,
  syncTrakt,
  type TraktSyncResult,
} from "../api/trakt";

function fmtTime(unix: number): string {
  return unix ? new Date(unix * 1000).toLocaleString() : "";
}

function SyncResultLine({ r }: { r: TraktSyncResult }) {
  if (r.skipped) {
    return (
      <span className="text-xs text-ink-dim">
        上次同步：Trakt 无新变化，已跳过
      </span>
    );
  }
  return (
    <span className="text-xs text-ink-dim">
      Trakt 已看 {r.total} 部 · 新标记 {r.newly_watched} · 时间戳更新{" "}
      {r.timestamp_advanced} · 已看过 {r.already_watched} · 未匹配 {r.unmatched}
      {r.unmatched_titles && r.unmatched_titles.length > 0 && (
        <span
          className="ml-1 underline decoration-dotted"
          title={`未匹配：${r.unmatched_titles.join("、")}${r.unmatched > r.unmatched_titles.length ? " …" : ""}`}
        >
          ?
        </span>
      )}
    </span>
  );
}

export default function TraktSyncCard() {
  const qc = useQueryClient();
  const { data: st } = useQuery({
    queryKey: ["trakt-status"],
    queryFn: getTraktStatus,
    // while a device flow is pending, poll to drive the connect forward
    refetchInterval: (query) =>
      query.state.data?.pending
        ? Math.max((query.state.data.pending.interval || 5) * 1000, 3000)
        : false,
  });

  const invalidate = () => qc.invalidateQueries({ queryKey: ["trakt-status"] });

  const connectMut = useMutation({
    mutationFn: connectTrakt,
    onSuccess: invalidate,
  });
  const syncMut = useMutation({
    mutationFn: syncTrakt,
    onSuccess: () => {
      invalidate();
      // watched badges / filters changed
      qc.invalidateQueries({ queryKey: ["movies"] });
    },
  });
  const disconnectMut = useMutation({
    mutationFn: disconnectTrakt,
    onSuccess: invalidate,
  });

  if (st && !st.configured) {
    return (
      <section className="mt-6 rounded-lg border border-border bg-bg-panel p-5">
        <h2 className="mb-3 flex items-center gap-2 font-semibold text-ink">
          <Link2 className="h-4 w-4" /> Trakt 观看状态同步
        </h2>
        <p className="text-sm text-ink-muted">
          未配置。设置环境变量{" "}
          <code className="rounded bg-bg px-1">TRAKT_CLIENT_ID</code> /{" "}
          <code className="rounded bg-bg px-1">TRAKT_CLIENT_SECRET</code>{" "}
          （在 trakt.tv 创建 API 应用获取）并重启后可用。
        </p>
      </section>
    );
  }

  // device flow in flight: show the code + activation link
  if (st?.pending) {
    return (
      <section className="mt-6 rounded-lg border border-border bg-bg-panel p-5">
        <h2 className="mb-3 flex items-center gap-2 font-semibold text-ink">
          <RefreshCw className="h-4 w-4 animate-spin" /> 连接 Trakt
        </h2>
        <p className="text-sm text-ink-muted">
          打开{" "}
          <a
            href={`${st.pending.verification_url}/${st.pending.user_code}`}
            target="_blank"
            rel="noreferrer"
            className="text-accent underline"
          >
            {st.pending.verification_url}
          </a>{" "}
          并输入以下代码：
        </p>
        <p className="my-3 text-center text-3xl font-mono font-bold tracking-widest text-ink">
          {st.pending.user_code}
        </p>
        <p className="text-xs text-ink-dim">等待授权…（代码 10 分钟内有效）</p>
      </section>
    );
  }

  const connected = st?.connected ?? false;
  const lastResult = st?.last_result;

  return (
    <section className="mt-6 rounded-lg border border-border bg-bg-panel p-5">
      <h2 className="mb-3 flex items-center gap-2 font-semibold text-ink">
        <Link2 className="h-4 w-4" /> Trakt 观看状态同步
      </h2>

      {connected ? (
        <div className="flex items-center gap-3">
          <div className="flex-1">
            <div className="font-medium text-ink">
              已连接：{st?.username || "trakt 用户"}
            </div>
            <div className="mt-1 text-xs text-ink-dim">
              {st?.last_sync_at
                ? `上次同步：${fmtTime(st.last_sync_at)}`
                : "从未同步"}
            </div>
            {lastResult && (
              <div className="mt-1">
                <SyncResultLine r={lastResult} />
              </div>
            )}
          </div>
          <button
            onClick={() => syncMut.mutate()}
            disabled={syncMut.isPending}
            className="inline-flex items-center gap-1 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-white disabled:opacity-40"
          >
            <RefreshCw
              className={syncMut.isPending ? "h-4 w-4 animate-spin" : "h-4 w-4"}
            />
            {syncMut.isPending ? "同步中…" : "立即同步"}
          </button>
          <button
            onClick={() => disconnectMut.mutate()}
            disabled={disconnectMut.isPending}
            className="inline-flex items-center gap-1 rounded-md border border-border px-3 py-1.5 text-sm text-ink-muted hover:text-ink disabled:opacity-40"
          >
            <Unlink className="h-4 w-4" />
            断开
          </button>
        </div>
      ) : (
        <div className="flex items-center gap-3">
          <p className="flex-1 text-sm text-ink-muted">
            将你在 trakt.tv 的观看记录同步到本地库（只标记"已看"，不会改动本地已有状态）。
          </p>
          <button
            onClick={() => connectMut.mutate()}
            disabled={connectMut.isPending}
            className="inline-flex items-center gap-1 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-white disabled:opacity-40"
          >
            <Link2 className="h-4 w-4" />
            {connectMut.isPending ? "请求中…" : "连接 Trakt"}
          </button>
        </div>
      )}

      {syncMut.isSuccess && syncMut.data && (
        <p className="mt-2 inline-flex items-center gap-1 text-xs text-emerald-400">
          <CheckCircle2 className="h-4 w-4" />
          同步完成 · <SyncResultLine r={syncMut.data} />
        </p>
      )}
      {(connectMut.isError || syncMut.isError || disconnectMut.isError) && (
        <p className="mt-2 text-sm text-accent">
          {String(connectMut.error || syncMut.error || disconnectMut.error)}
        </p>
      )}
    </section>
  );
}
