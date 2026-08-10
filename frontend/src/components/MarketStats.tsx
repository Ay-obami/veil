import { useRef } from 'react';
import { formatPrice, formatHumanAdaptive } from '../lib/price';
import type { BookMatch } from '../lib/orderbook';
import { PairSelector } from './PairSelector';

interface PriceLevel {
  price: number;
  quantity: number;
}

interface MarketStatsProps {
  pair: string;
  pairs: string[];
  onPairChange: (pair: string) => void;
  bids: PriceLevel[];
  asks: PriceLevel[];
  matches: BookMatch[];
}

export function MarketStats({ pair, pairs, onPairChange, bids, asks, matches }: MarketStatsProps) {
  const [base, quote] = pair.split('/');

  const bestBidRaw = bids.length > 0 ? Math.max(...bids.map(b => b.price)) : 0;
  const bestAskRaw = asks.length > 0 ? Math.min(...asks.map(a => a.price)) : 0;
  const midRaw = bestBidRaw && bestAskRaw ? (bestBidRaw + bestAskRaw) / 2 : 0;
  const mid = midRaw ? formatPrice(midRaw) : 0;

  const matchPricesRaw = matches.map(m => m.price);
  const high24Raw = matchPricesRaw.length ? Math.max(...matchPricesRaw) : 0;
  const low24Raw  = matchPricesRaw.length ? Math.min(...matchPricesRaw) : 0;
  const high24 = high24Raw ? formatPrice(high24Raw) : 0;
  const low24  = low24Raw  ? formatPrice(low24Raw)  : 0;

  // 24h volumes: base = Σ qty, quote = Σ price*qty
  const volBase = matches.reduce((s, m) => s + m.quantity, 0);
  const volQuote = matches.reduce((s, m) => s + formatPrice(m.price) * m.quantity, 0);

  // Display price: last trade if available, else mid
  const lastMatchRaw = matches.length > 0 ? matches[0].price : 0;
  const displayPrice = lastMatchRaw ? formatPrice(lastMatchRaw) : mid;

  // Change: oldest match in buffer as session reference
  const refMatchRaw = matches.length > 1 ? matches[matches.length - 1].price : 0;
  const refPrice = refMatchRaw ? formatPrice(refMatchRaw) : 0;
  const absChange = (refPrice && displayPrice) ? displayPrice - refPrice : 0;
  const pctChange = (refPrice && displayPrice) ? (absChange / refPrice) * 100 : 0;

  // Persist last tick direction so the price stays colored between ticks.
  const prevPrice = useRef(displayPrice);
  const lastDir = useRef<'up' | 'dn' | ''>('');
  if (displayPrice !== prevPrice.current) {
    lastDir.current = displayPrice > prevPrice.current ? 'up' : 'dn';
    prevPrice.current = displayPrice;
  }
  const dir = lastDir.current || (absChange >= 0 ? 'up' : 'dn');

  const fmtPrice = (n: number) => n > 0 ? formatHumanAdaptive(n) : '—';
  const fmtBase = (n: number) => n > 0 ? n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 4 }) : '—';
  const fmtQuote = (n: number) => n > 0 ? '$' + n.toLocaleString(undefined, { maximumFractionDigits: 0 }) : '—';
  const changeDir = absChange > 0 ? 'up' : absChange < 0 ? 'dn' : '';
  const changeSign = absChange >= 0 ? '+' : '';

  return (
    <div className="mstats">
      <PairSelector pair={pair} pairs={pairs} onPairChange={onPairChange} />
      <div className={`mstats-mid ${dir}`}>
        <div className={`price ${dir}`}>{fmtPrice(displayPrice)}</div>
        <div className={`price-change ${changeDir || 'up'}`}>
          <span>{changeSign}{formatHumanAdaptive(absChange)}</span>
          <span className="pct">{changeSign}{pctChange.toFixed(2)}%</span>
        </div>
      </div>
      <div className="mstats-stats">
        <div className="mstats-stat">
          <div className="label">24H HIGH</div>
          <div className="value">{fmtPrice(high24)}</div>
        </div>
        <div className="mstats-stat">
          <div className="label">24H LOW</div>
          <div className="value">{fmtPrice(low24)}</div>
        </div>
        <div className="mstats-stat">
          <div className="label">24H VOL {base}</div>
          <div className="value">{fmtBase(volBase)}</div>
        </div>
        <div className="mstats-stat">
          <div className="label">24H VOL {quote}</div>
          <div className="value">{fmtQuote(volQuote)}</div>
        </div>
      </div>
    </div>
  );
}
