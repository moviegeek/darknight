// Small formatting helpers shared across components.

export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

export function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return "-";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 2 : 1)} ${units[i]}`;
}

export function formatDuration(seconds: number): string {
  if (!seconds || seconds <= 0) return "-";
  const h = Math.floor(seconds / 3600);
  const m = Math.round((seconds % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function formatBitrate(bps: number): string {
  if (!bps || bps <= 0) return "-";
  const mbps = bps / 1_000_000;
  if (mbps >= 1) return `${mbps.toFixed(1)} Mbps`;
  return `${(bps / 1000).toFixed(0)} kbps`;
}

export function resolutionLabel(res: string): string {
  switch (res) {
    case "2160p":
      return "4K";
    case "1080p":
      return "1080p";
    case "720p":
      return "720p";
    default:
      return res || "-";
  }
}

// Maps a raw source string to a display label. Disc-folder releases are
// tagged "Bluray Disk" by the scanner; show them as "蓝光原盘".
export function sourceLabel(src: string): string {
  switch (src) {
    case "Bluray Disk":
      return "蓝光原盘";
    case "UHD BluRay":
      return "UHD BluRay";
    case "BluRay":
      return "BluRay";
    case "WebDL":
      return "WebDL";
    case "HDTV":
      return "HDTV";
    default:
      return src || "-";
  }
}

export function channelsLabel(ch: string): string {
  return ch || "-";
}

// Maps an ffprobe channel count to the conventional speaker layout label.
export function audioChannelCountLabel(n: number): string {
  switch (n) {
    case 1:
      return "1.0";
    case 2:
      return "2.0";
    case 6:
      return "5.1";
    case 8:
      return "7.1";
    default:
      return n > 0 ? `${n}ch` : "";
  }
}

// Converts an ISO 3166-1 alpha-2 country code (e.g. "JP") to its flag emoji by
// mapping each letter to its regional indicator symbol. Returns "" for
// anything that isn't a two-letter code.
export function countryFlagEmoji(iso2: string): string {
  const code = (iso2 || "").trim().toUpperCase();
  if (code.length !== 2) return "";
  const base = 0x1f1e6; // regional indicator symbol letter A
  const points = [...code].map((c) => base + (c.charCodeAt(0) - 65));
  if (points.some((cp) => cp < base || cp > base + 25)) return "";
  return String.fromCodePoint(...points);
}

// Returns the deduped list of ISO country codes for a movie, preferring the
// full `countries` list (comma-separated) and falling back to the single
// `country` field for older/unenriched rows.
export function movieCountryCodes(m: { country?: string; countries?: string }): string[] {
  const raw = m.countries || m.country || "";
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of raw.split(",")) {
    const code = part.trim();
    if (code && !seen.has(code)) {
      seen.add(code);
      out.push(code);
    }
  }
  return out;
}

// Bilingual title display. The primary line is the original title (with
// fallbacks); the secondary line is the "other" language title per the rule:
//   original English  -> Chinese
//   original Chinese  -> English
//   other             -> English + Chinese
// A secondary value equal to the primary is omitted.
export function movieTitleLines(m: {
  title: string;
  original_title?: string;
  title_en?: string;
  title_zh?: string;
  original_language?: string;
}): { primary: string; secondary: string } {
  const primary = m.original_title || m.title_en || m.title_zh || m.title || "";
  const ol = (m.original_language || "").slice(0, 2).toLowerCase();
  let secondary = "";
  if (ol === "en") {
    secondary = m.title_zh && m.title_zh !== primary ? m.title_zh : "";
  } else if (ol === "zh" || ol === "cn") {
    secondary = m.title_en && m.title_en !== primary ? m.title_en : "";
  } else {
    secondary = [m.title_en, m.title_zh]
      .filter((t) => t && t !== primary)
      .join(" · ");
  }
  return { primary, secondary };
}
