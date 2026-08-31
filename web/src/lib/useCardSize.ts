import { useCallback, useEffect, useState } from "react";

// Storage key + default for the global card-grid density preference. Shared
// by the library page and the collection pages so one slider choice applies
// everywhere, and persisted so the size survives navigating away and back
// (which unmounts the page) as well as reloads.
const CARD_SIZE_KEY = "darknight:cardSize";

// The slider snaps to these fixed widths (px) instead of a free-form range.
// The UI presents them by name (小/中/大/超大), never as raw pixel numbers.
export const CARD_SIZES = [120, 160, 220, 300] as const;
export type CardSize = (typeof CARD_SIZES)[number];

export const DEFAULT_CARD_SIZE: CardSize = 160;

function readStored(): CardSize {
  try {
    const raw = localStorage.getItem(CARD_SIZE_KEY);
    if (!raw) return DEFAULT_CARD_SIZE;
    const n = Number(raw);
    return (CARD_SIZES as readonly number[]).includes(n) ? (n as CardSize) : DEFAULT_CARD_SIZE;
  } catch {
    return DEFAULT_CARD_SIZE;
  }
}

// useCardSize returns the global card-size preference and a setter, backed by
// localStorage so it survives navigation and reloads. A storage event listener
// keeps multiple open tabs in sync.
export function useCardSize(): [CardSize, (s: CardSize) => void] {
  const [size, setSize] = useState<CardSize>(readStored);

  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === CARD_SIZE_KEY) setSize(readStored());
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const set = useCallback((s: CardSize) => {
    setSize(s);
    try {
      localStorage.setItem(CARD_SIZE_KEY, String(s));
    } catch {
      // ignore quota / disabled storage
    }
  }, []);

  return [size, set];
}
