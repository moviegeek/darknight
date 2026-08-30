// Trakt.tv watch-status sync API. Self-contained module (own types) so the
// feature does not entangle with the shared client surface.
import { http } from "./client";

export interface TraktConnectInfo {
  user_code: string;
  verification_url: string;
  expires_at: number; // unix seconds
  interval: number; // poll interval, seconds
}

export interface TraktSyncResult {
  total: number; // watched entries on Trakt
  matched: number; // matched a local movie
  newly_watched: number; // rows written (new or upgraded)
  timestamp_advanced: number; // existing watched rows moved forward
  already_watched: number; // already watched, nothing to do
  unmatched: number; // on Trakt, not in the library
  unmatched_titles?: string[];
  skipped: boolean; // no remote change since last sync
  at: number;
}

export interface TraktStatus {
  configured: boolean;
  connected: boolean;
  username: string;
  pending?: TraktConnectInfo;
  last_sync_at: number;
  last_result?: TraktSyncResult;
}

export const getTraktStatus = () => http<TraktStatus>("/api/trakt/status");
export const connectTrakt = () =>
  http<TraktConnectInfo>("/api/trakt/connect", { method: "POST" });
export const syncTrakt = () =>
  http<TraktSyncResult>("/api/trakt/sync", { method: "POST" });
export const disconnectTrakt = () =>
  http<void>("/api/trakt/disconnect", { method: "POST" });
