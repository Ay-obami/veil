import { useRef } from 'react';
import { formatUnits } from 'viem';
import { formatPrice, formatPriceAdaptive } from '../lib/price';

interface PriceLevel {
  price: number;
  quantity: number;
}

interface OrderBookProps {
  bids: PriceLevel[];
  asks: PriceLevel[];
  baseDecimals?: number;
  baseSymbol?: string;
  quoteSymbol?: string;
  onPriceClick: (price: number, side: 'buy' | 'sell') => void;
}

const ROWS = 14;

function formatQty(raw: number, decimals?: number): string {
  if (decimals === undefined) return String(raw);
  return parseFloat(formatUnits(BigInt(Math.floor(raw)), decimals)).toFixed(4);
}

export function OrderBook({
  bids,
  asks,
  baseDecimals,
  baseSymbol,
  quoteSymbol,
  onPriceClick,
}: OrderBookProps) {
  // Best ask = lowest, best bid = highest. Keep a fixed row count per side.
  const sortedAsks = [...asks].sort((a, b) => a.price - b.price).slice(0, ROWS);
  const sortedBids = [...bids].sort((a, b) => b.price - a.price).slice(0, ROWS);

  // Cumulative sizes from the best price outward.
  let cum = 0;
  const askCum = sortedAsks.map(a => (cum += a.quantity));
  cum = 0;
  const bidCum = sortedBids.map(b => (cum += b.quantity));

  const bidTotal = bidCum[bidCum.length - 1] ?? 0;
  const askTotal = askCum[askCum.length - 1] ?? 0;
  const maxCum = Math.max(bidTotal, askTotal, 1);

  const bestBidRaw = sortedBids[0]?.price ?? 0;
  const bestAskRaw = sortedAsks[0]?.price ?? 0;
  const midRaw = bestBidRaw && bestAskRaw ? (bestBidRaw + bestAskRaw) / 2 : 0;
  const mid = midRaw ? formatPrice(midRaw) : 0;
  const spreadRaw = bestBidRaw && bestAskRaw ? bestAskRaw - bestBidRaw : 0;
  const spread = spreadRaw ? formatPrice(spreadRaw) : 0;
  const spreadBps = mid > 0 ? (spread / mid) * 10000 : 0;

  const prevMidRef = useRef(mid);
  const dirRef = useRef<'up' | 'dn'>('up');
  if (mid !== prevMidRef.current) {
    dirRef.current = mid >= prevMidRef.current ? 'up' : 'dn';
    prevMidRef.current = mid;
  }

  const fmtPrice = (raw: number) => formatPriceAdaptive(raw);
  const baseUnit = baseSymbol ? ` · ${baseSymbol}` : '';
  const quoteUnit = quoteSymbol ? ` · ${quoteSymbol}` : '';

  // Asks: display with largest total at top, best ask at bottom (near spread).
  const askRows = sortedAsks.map((lvl, i) => ({ lvl, cum: askCum[i] })).reverse();

  const bidShare = bidTotal + askTotal > 0 ? (bidTotal / (bidTotal + askTotal)) * 100 : 50;

  return (
    <div className="ob">
      <div className="ob-head">
        <span>PRICE{quoteUnit}</span>
        <span>SIZE{baseUnit}</span>
        <span>SUM{baseUnit}</span>
      </div>

      <div className="ob-side asks">
        {askRows.map(({ lvl, cum }, i) => {
          const humanPrice = formatPrice(lvl.price);
          const pct = (cum / maxCum) * 100;
          return (
            <div
              key={`a-${lvl.price}-${i}`}
              className="ob-row ask"
              onClick={() => onPriceClick(humanPrice, 'buy')}
            >
              <div className="bar" style={{ width: `${pct}%` }} />
              <span className="price">{fmtPrice(lvl.price)}</span>
              <span className="size">{formatQty(lvl.quantity, baseDecimals)}</span>
              <span className="total">{formatQty(cum, baseDecimals)}</span>
            </div>
          );
        })}
      </div>

      <div className="ob-spread">
        <span className={`ob-mid ${dirRef.current}`}>
          {midRaw ? formatPriceAdaptive(midRaw) : '—'}
        </span>
        <span className="ob-spread-meta">
          <span className="dim">SPREAD</span> {spreadRaw ? formatPriceAdaptive(spreadRaw) : '—'}
          {spread > 0 && (
            <> <span className="dim">/</span> {spreadBps.toFixed(1)} bps</>
          )}
        </span>
      </div>

      <div className="ob-side bids">
        {sortedBids.map((lvl, i) => {
          const humanPrice = formatPrice(lvl.price);
          const pct = (bidCum[i] / maxCum) * 100;
          return (
            <div
              key={`b-${lvl.price}-${i}`}
              className="ob-row bid"
              onClick={() => onPriceClick(humanPrice, 'sell')}
            >
              <div className="bar" style={{ width: `${pct}%` }} />
              <span className="price">{fmtPrice(lvl.price)}</span>
              <span className="size">{formatQty(lvl.quantity, baseDecimals)}</span>
              <span className="total">{formatQty(bidCum[i], baseDecimals)}</span>
            </div>
          );
        })}
      </div>

      <div className="ob-foot">
        <div className="ob-foot-bar">
          <div className="ob-foot-bar-bid" style={{ width: `${bidShare}%` }} />
          <div className="ob-foot-bar-ask" style={{ width: `${100 - bidShare}%` }} />
        </div>
        <div className="ob-foot-meta">
          <span><span className="dim">B</span> {formatQty(bidTotal, baseDecimals)}</span>
          <span><span className="dim">A</span> {formatQty(askTotal, baseDecimals)}</span>
        </div>
      </div>
    </div>
  );
}
