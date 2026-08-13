import { useCallback, useState } from "react";

// Persisted recent-SQL list for the developer console. Backed by localStorage
// so history survives navigation and reloads. Capped at MAX_HISTORY entries and
// deduped (re-running a query bumps it to the front).
const HISTORY_KEY = "darknight:sqlHistory";
const MAX_HISTORY = 20;

function readStored(): string[] {
  try {
    const raw = localStorage.getItem(HISTORY_KEY);
    if (!raw) return [];
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return [];
    return arr.filter((v) => typeof v === "string").slice(0, MAX_HISTORY);
  } catch {
    return [];
  }
}

// useSqlHistory returns the saved SQL history and a function to push a new
// query. Pushing prepends the query (moving it to front if it already exists)
// and trims to MAX_HISTORY.
export function useSqlHistory(): [string[], (q: string) => void] {
  const [history, setHistory] = useState<string[]>(readStored);

  const push = useCallback((q: string) => {
    const trimmed = q.trim();
    if (!trimmed) return;
    setHistory((prev) => {
      const next = [trimmed, ...prev.filter((h) => h !== trimmed)].slice(0, MAX_HISTORY);
      try {
        localStorage.setItem(HISTORY_KEY, JSON.stringify(next));
      } catch {
        // ignore quota / disabled storage
      }
      return next;
    });
  }, []);

  return [history, push];
}
