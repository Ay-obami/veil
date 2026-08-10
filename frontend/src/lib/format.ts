/**
 * format.ts — uint64 <-> decimal helpers for price/quantity display.
 *
 * The extension uses raw uint64 token units. This module converts between
 * display decimals (e.g. "12.5") and raw units, respecting token decimals.
 */

/**
 * Convert a raw uint64 amount to a display string with the given decimals.
 * E.g. toDisplay(1500000, 6) => "1.5"
 */
export function toDisplay(raw: number | bigint, decimals: number): string {
  if (decimals === 0) return String(raw);
  const divisor = 10 ** decimals;
  const num = Number(raw) / divisor;
  // Remove trailing zeros but keep at least some precision.
  return num.toFixed(decimals).replace(/\.?0+$/, "") || "0";
}

/**
 * Convert a display string to raw uint64 units.
 * E.g. toRaw("1.5", 6) => 1500000
 */
export function toRaw(display: string, decimals: number): number {
  const num = parseFloat(display);
  if (isNaN(num) || num < 0) return 0;
  const raw = Math.round(num * 10 ** decimals);
  // Guard against uint64 overflow.
  if (raw > Number.MAX_SAFE_INTEGER) {
    throw new Error("Amount too large");
  }
  return raw;
}

/**
 * Format a price for display in the orderbook ladder.
 * Shows up to `maxDecimals` significant decimal places.
 */
export function formatPrice(
  raw: number,
  decimals: number,
  maxDecimals = 4
): string {
  if (decimals === 0) return String(raw);
  const num = Number(raw) / 10 ** decimals;
  return num.toLocaleString(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: maxDecimals,
  });
}

/**
 * Format a quantity for display.
 */
export function formatQuantity(
  raw: number,
  decimals: number,
  maxDecimals = 4
): string {
  return formatPrice(raw, decimals, maxDecimals);
}
