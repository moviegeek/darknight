import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, FileText, Loader2, Upload, X } from "lucide-react";

import { uploadSubtitles } from "../api/client";
import type { MovieFile } from "../api/types";
import { cn, formatBytes } from "../lib/format";

// The languages the upload dialog offers. Values are display names; the
// backend resolves them to ISO 639-2 codes (chi/yue/eng/...).
const LANGUAGES = [
  "中文",
  "粤语",
  "英文",
  "日文",
  "韩文",
  "法语",
  "德语",
  "西班牙语",
  "意大利语",
  "葡萄牙语",
  "俄语",
  "泰语",
  "其他",
];

const ALLOWED_EXTS = [".srt", ".ass", ".ssa"];

// UploadSubtitlesButton opens the upload dialog. Shown per release (file
// version) on the movie page since the subtitle lands next to that release's
// video file.
export function UploadSubtitlesButton({
  movieId,
  file,
  compact,
}: {
  movieId: number;
  file: MovieFile;
  compact?: boolean;
}) {
  const [open, setOpen] = useState(false);
  if (file.is_disc) return null;

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className={cn(
          "inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1 text-xs text-ink-muted transition hover:text-ink"
        )}
        title="上传字幕文件（srt/ass/ssa），放到该版本目录下"
      >
        <Upload className="h-3.5 w-3.5" />
        {compact ? "字幕" : "上传字幕"}
      </button>
      {open && (
        <UploadDialog movieId={movieId} file={file} onClose={() => setOpen(false)} />
      )}
    </>
  );
}

function UploadDialog({
  movieId,
  file,
  onClose,
}: {
  movieId: number;
  file: MovieFile;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const inputRef = useRef<HTMLInputElement>(null);
  const [items, setItems] = useState<Array<{ file: File; lang: string }>>([]);
  const [error, setError] = useState("");

  const addFiles = (fl: FileList | null) => {
    if (!fl) return;
    const next: Array<{ file: File; lang: string }> = [];
    for (const f of Array.from(fl)) {
      const ext = f.name.slice(f.name.lastIndexOf(".")).toLowerCase();
      if (!ALLOWED_EXTS.includes(ext)) {
        setError(`不支持的格式 ${ext}（仅支持 srt/ass/ssa）：${f.name}`);
        continue;
      }
      next.push({ file: f, lang: "中文" });
    }
    if (next.length > 0) {
      setError("");
      setItems((prev) => [...prev, ...next]);
    }
  };

  const upload = useMutation({
    mutationFn: () =>
      uploadSubtitles(
        movieId,
        file.id,
        items.map((i) => i.file),
        items.map((i) => i.lang)
      ),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["movie-files", movieId] });
      await qc.invalidateQueries({ queryKey: ["movie-file", movieId] });
      await qc.invalidateQueries({ queryKey: ["movies"] });
      onClose();
    },
    onError: (e) => setError((e as Error).message),
  });

  const setLang = (idx: number, lang: string) =>
    setItems((prev) => prev.map((it, i) => (i === idx ? { ...it, lang } : it)));

  const removeItem = (idx: number) =>
    setItems((prev) => prev.filter((_, i) => i !== idx));

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/60 p-6"
      onClick={onClose}
    >
      <div
        className="mt-12 w-full max-w-xl rounded-lg border border-border bg-bg-panel p-5 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-1 flex items-center justify-between">
          <h3 className="text-base font-semibold text-ink">上传字幕</h3>
          <button onClick={onClose} className="text-ink-dim hover:text-ink">
            <X className="h-4 w-4" />
          </button>
        </div>
        <p className="mb-4 break-all font-mono text-xs text-ink-dim">
          {file.library_name}/{file.dir_path}
        </p>

        {/* drop zone / picker */}
        <button
          onClick={() => inputRef.current?.click()}
          className="flex w-full flex-col items-center gap-1.5 rounded-md border border-dashed border-border py-6 text-sm text-ink-muted transition hover:border-ink-dim hover:text-ink"
        >
          <Upload className="h-5 w-5" />
          点击选择字幕文件（可多选，srt / ass / ssa）
        </button>
        <input
          ref={inputRef}
          type="file"
          multiple
          accept=".srt,.ass,.ssa"
          className="hidden"
          onChange={(e) => {
            addFiles(e.target.files);
            e.target.value = "";
          }}
        />

        {/* staged files with language pickers */}
        {items.length > 0 && (
          <ul className="mt-4 space-y-2">
            {items.map((it, idx) => (
              <li
                key={`${it.file.name}-${idx}`}
                className="flex items-center gap-2 rounded-md border border-border px-2.5 py-2"
              >
                <FileText className="h-4 w-4 shrink-0 text-ink-dim" />
                <span className="min-w-0 flex-1 truncate text-sm text-ink" title={it.file.name}>
                  {it.file.name}
                </span>
                <span className="shrink-0 text-xs text-ink-dim">
                  {formatBytes(it.file.size)}
                </span>
                <select
                  value={it.lang}
                  onChange={(e) => setLang(idx, e.target.value)}
                  className="shrink-0 rounded-md border border-border bg-bg px-2 py-1 text-xs text-ink outline-none"
                >
                  {LANGUAGES.map((l) => (
                    <option key={l} value={l}>
                      {l}
                    </option>
                  ))}
                </select>
                <button
                  onClick={() => removeItem(idx)}
                  className="shrink-0 text-ink-dim hover:text-rose-300"
                  title="移除"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </li>
            ))}
          </ul>
        )}

        {error && <p className="mt-3 text-sm text-rose-300">{error}</p>}

        {/* actions */}
        <div className="mt-4 flex items-center justify-end gap-2 border-t border-border pt-3">
          <button
            onClick={onClose}
            className="rounded-md border border-border px-3 py-1.5 text-sm text-ink-muted hover:text-ink"
          >
            取消
          </button>
          <button
            onClick={() => upload.mutate()}
            disabled={items.length === 0 || upload.isPending}
            className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-bg disabled:opacity-60"
          >
            {upload.isPending ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Check className="h-4 w-4" />
            )}
            确认上传（{items.length}）
          </button>
        </div>
      </div>
    </div>
  );
}
