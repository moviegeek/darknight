import { useCallback, useEffect, useState } from "react";
import type { MovieQuery } from "../api/types";

// Storage key + default for the library page's active filters / sort. The
// query lives in component state on LibraryPage, but component state is
// dropped every time the page unmounts - which happens when the user opens a
// movie detail page and navigates back. Persisting it to localStorage keeps
// the selected filters and sort across that round-trip (and across reloads),
// and the storage event listener keeps multiple open tabs in sync.
const QUERY_KEY = "darknight:libraryQuery";
const QUERY_DEFAULT: MovieQuery = { sort: "title", limit: 0, offset: 0 };

// offset is view state (which page of results is shown), not a filter, so it
// is never persisted - returning to the library should always start at page 0.
function sanitize(stored: Partial<MovieQuery>): MovieQuery {
  const { offset: _offset, ...rest } = stored;
  return { ...QUERY_DEFAULT, ...rest, offset: 0 };
}

function readStored(): MovieQuery {
  try {
    const raw = localStorage.getItem(QUERY_KEY);
    if (!raw) return { ...QUERY_DEFAULT };
    return sanitize(JSON.parse(raw) as Partial<MovieQuery>);
  } catch {
    return { ...QUERY_DEFAULT };
  }
}

// useLibraryQuery returns the persisted library query and a setter, backed by
// localStorage so it survives navigation and reloads. A storage event
// listener keeps multiple open tabs in sync.
export function useLibraryQuery(): [MovieQuery, (q: MovieQuery) => void] {
  const [query, setQuery] = useState<MovieQuery>(readStored);

  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === QUERY_KEY) setQuery(readStored());
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const set = useCallback((q: MovieQuery) => {
    setQuery(q);
    try {
      // offset is stripped on write too, so a stale "page 3" never lands in
      // storage if a future code path forgets to reset it.
      localStorage.setItem(QUERY_KEY, JSON.stringify(sanitize(q)));
    } catch {
      // ignore quota / disabled storage
    }
  }, []);

  return [query, set];
}
