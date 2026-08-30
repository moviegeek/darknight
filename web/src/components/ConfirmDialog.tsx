import { useEffect } from "react";
import { AlertTriangle, X } from "lucide-react";

// ConfirmDialog is a modal with a backdrop for destructive actions. Closes on
// backdrop click or Escape.
export function ConfirmDialog({
  title,
  message,
  confirmLabel,
  cancelLabel,
  onConfirm,
  onCancel,
}: {
  title: string;
  message: string;
  confirmLabel: string;
  cancelLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onCancel}
    >
      <div
        className="mx-4 w-full max-w-md rounded-lg border border-border bg-bg-panel p-5 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-start gap-3">
          <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-accent" />
          <div>
            <h3 className="font-semibold text-ink">{title}</h3>
            <p className="mt-1.5 text-sm text-ink-muted">{message}</p>
          </div>
        </div>
        <div className="mt-5 flex justify-end gap-2">
          <button
            onClick={onCancel}
            className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm text-ink-muted hover:text-ink"
          >
            <X className="h-3.5 w-3.5" />
            {cancelLabel}
          </button>
          <button
            onClick={onConfirm}
            className="rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-white hover:bg-accent/90"
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
