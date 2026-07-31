import { useCallback, useEffect, useState } from "react";

// Storage key + default for the global "显示缺失影片" preference. Shared
// between the collections list page (where the toggle lives) and the
// collection detail page (where it is applied), so toggling it on the list
// page carries over to every detail page the user opens afterwards.
const SHOW_MISSING_KEY = "darknight:showMissingCollections";
const SHOW_MISSING_DEFAULT = false;

function readStored(): boolean {
  try {
    const v = localStorage.getItem(SHOW_MISSING_KEY);
    if (v === null) return SHOW_MISSING_DEFAULT;
    return v === "1";
  } catch {
    return SHOW_MISSING_DEFAULT;
  }
}

// useShowMissing returns the global "show missing films" preference and a
// setter, backed by localStorage so it survives navigation and reloads. A
// storage event listener keeps multiple open tabs in sync.
export function useShowMissing(): [boolean, (v: boolean) => void] {
  const [showMissing, setShowMissing] = useState<boolean>(readStored);

  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === SHOW_MISSING_KEY) setShowMissing(readStored());
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const set = useCallback((v: boolean) => {
    setShowMissing(v);
    try {
      localStorage.setItem(SHOW_MISSING_KEY, v ? "1" : "0");
    } catch {
      // ignore quota / disabled storage
    }
  }, []);

  return [showMissing, set];
}
