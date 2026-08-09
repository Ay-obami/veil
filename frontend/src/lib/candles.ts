import type { BookMatch, ServerCandle } from "./orderbook";
import { formatPrice } from "./price";

export type Timeframe = "1m" | "5m" | "15m" | "1h" | "4h" | "1D";

export const TIMEFRAMES: Timeframe[] = ["1m", "5m", "15m", "1h", "4h", "1D"];

export const TF_SECONDS: Record<Timeframe, number> = {
  "1m": 60,
  "5m": 5 * 60,
  "15m": 15 * 60,
  "1h": 60 * 60,
  "4h": 4 * 60 * 60,
  "1D": 24 * 60 * 60,
};

export interface Candle {
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
}

function toSeconds(ts: number): number {
  if (ts > 1e15) return Math.floor(ts / 1e9); // nanoseconds
  if (ts > 1e12) return Math.floor(ts / 1000); // milliseconds
  return Math.floor(ts);                       // seconds
}

/**
 * Group raw matches into OHLC candles for the given timeframe.
 * Empty intervals between trades carry the previous close as a flat candle.
 * Output is sorted ascending by time and capped to the most recent `maxCandles`.
 */
export function bucketMatches(
  matches: BookMatch[],
  tf: Timeframe,
  maxCandles = 240,
): Candle[] {
  if (!matches.length) return [];

  const step = TF_SECONDS[tf];
  const sorted = [...matches].sort((a, b) => toSeconds(a.timestamp) - toSeconds(b.timestamp));

  const buckets = new Map<number, Candle>();
  for (const m of sorted) {
    const secs = toSeconds(m.timestamp);
    if (!Number.isFinite(secs) || secs <= 0) continue;
    const t = Math.floor(secs / step) * step;
    const price = formatPrice(m.price);
    const existing = buckets.get(t);
    if (!existing) {
      buckets.set(t, {
        time: t,
        open: price,
        high: price,
        low: price,
        close: price,
        volume: m.quantity,
      });
    } else {
      existing.high = Math.max(existing.high, price);
      existing.low = Math.min(existing.low, price);
      existing.close = price;
      existing.volume += m.quantity;
    }
  }

  if (buckets.size === 0) return [];

  const keys = [...buckets.keys()].sort((a, b) => a - b);
  const first = keys[0];
  const last = keys[keys.length - 1];

  const out: Candle[] = [];
  let prevClose = buckets.get(first)!.open;
  for (let t = first; t <= last; t += step) {
    const c = buckets.get(t);
    if (c) {
      out.push(c);
      prevClose = c.close;
    } else {
      out.push({
        time: t,
        open: prevClose,
        high: prevClose,
        low: prevClose,
        close: prevClose,
        volume: 0,
      });
    }
  }

  return out.length > maxCandles ? out.slice(out.length - maxCandles) : out;
}

/**
 * Convert server-side OHLCV candles into chart Candles. Empty intervals between
 * server candles are gap-filled with flat candles carrying the previous close,
 * so a sparse market still draws as a continuous chart.
 */
export function fromServerCandles(
  serverCandles: ServerCandle[],
  tf: Timeframe,
  maxCandles = 240,
): Candle[] {
  if (!serverCandles.length) return [];

  const step = TF_SECONDS[tf];
  const sorted = [...serverCandles].sort((a, b) => a.openTime - b.openTime);

  const first = sorted[0].openTime;
  const last = sorted[sorted.length - 1].openTime;

  const byTime = new Map<number, ServerCandle>();
  for (const c of sorted) byTime.set(c.openTime, c);

  const out: Candle[] = [];
  let prevClose = formatPrice(sorted[0].open);
  for (let t = first; t <= last; t += step) {
    const c = byTime.get(t);
    if (c) {
      const close = formatPrice(c.close);
      out.push({
        time: t,
        open: formatPrice(c.open),
        high: formatPrice(c.high),
        low: formatPrice(c.low),
        close,
        volume: c.volume,
      });
      prevClose = close;
    } else {
      out.push({
        time: t,
        open: prevClose,
        high: prevClose,
        low: prevClose,
        close: prevClose,
        volume: 0,
      });
    }
  }

  return out.length > maxCandles ? out.slice(out.length - maxCandles) : out;
}
