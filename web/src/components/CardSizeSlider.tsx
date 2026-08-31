import { CARD_SIZES, type CardSize } from "../lib/useCardSize";

// Display names for the fixed card-size stops, index-aligned with CARD_SIZES.
const SIZE_NAMES = ["小", "中", "大", "超大"] as const;

// CardSizeSlider adjusts the movie/collection card grid density. It snaps to
// CARD_SIZES' fixed stops instead of a free-form pixel range: the range input
// works on the stop index, and the current size is shown by name, not px. The
// chosen size is persisted by the useCardSize hook that feeds this component.
export function CardSizeSlider({
  size,
  onChange,
}: {
  size: CardSize;
  onChange: (size: CardSize) => void;
}) {
  const index = Math.max(0, CARD_SIZES.indexOf(size));
  return (
    <div className="flex items-center gap-3 rounded-md border border-border bg-bg-panel px-3 py-1.5 text-xs text-ink-muted">
      <span>小</span>
      <input
        type="range"
        min={0}
        max={CARD_SIZES.length - 1}
        step={1}
        value={index}
        list="card-size-stops"
        onChange={(e) => onChange(CARD_SIZES[Number(e.target.value)])}
        className="w-32 accent-accent sm:w-40"
      />
      <span>大</span>
      <span className="ml-1 w-8 text-right text-ink">{SIZE_NAMES[index]}</span>
      {/* tick marks at the stops; browsers without datalist-on-range support
          simply render a plain stepped slider. */}
      <datalist id="card-size-stops">
        {CARD_SIZES.map((_, i) => (
          <option key={i} value={i} label={SIZE_NAMES[i]} />
        ))}
      </datalist>
    </div>
  );
}
