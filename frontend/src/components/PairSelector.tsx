import { useEffect, useRef, useState } from 'react';
import { useAllPairStats } from '../hooks/useAllPairStats';
import { formatHumanAdaptive } from '../lib/price';

const SYMBOL_NAMES: Record<string, string> = {
  FLR: 'Flare',
  BTC: 'Bitcoin',
  ETH: 'Ethereum',
  SOL: 'Solana',
  USDT: 'Tether',
  USDC: 'USD Coin',
  USD: 'US Dollar',
  ARB: 'Arbitrum',
  DOGE: 'Dogecoin',
  LINK: 'Chainlink',
};

function displayName(base: string, quote: string): string {
  const b = SYMBOL_NAMES[base];
  const q = SYMBOL_NAMES[quote];
  if (b && (quote === 'USD' || quote === 'USDT' || quote === 'USDC')) return b;
  if (b && q) return `${b} / ${q}`;
  return b ?? base;
}

function fmtPrice(n: number): string {
  return n ? formatHumanAdaptive(n) : '—';
}

function fmtChange(pct: number): string {
  if (!pct) return '—';
  const sign = pct >= 0 ? '+' : '';
  return `${sign}${pct.toFixed(2)}%`;
}

interface PairSelectorProps {
  pair: string;
  pairs: string[];
  onPairChange: (pair: string) => void;
}

export function PairSelector({ pair, pairs, onPairChange }: PairSelectorProps) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [ddPos, setDdPos] = useState<{ top: number; left: number } | null>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const ddRef = useRef<HTMLDivElement>(null);
  const stats = useAllPairStats();

  const [activeBase, activeQuote] = pair.split('/');

  useEffect(() => {
    if (!open) return;
    const place = () => {
      const r = triggerRef.current?.getBoundingClientRect();
      if (r) setDdPos({ top: r.bottom + 2, left: r.left });
    };
    place();
    const onDocDown = (e: MouseEvent) => {
      const t = e.target as Node;
      if (triggerRef.current?.contains(t)) return;
      if (ddRef.current?.contains(t)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    document.addEventListener('mousedown', onDocDown);
    document.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
      document.removeEventListener('mousedown', onDocDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const q = search.trim().toUpperCase();
  const filtered = q
    ? pairs.filter(p => p.toUpperCase().includes(q))
    : pairs;

  const handleSelect = (p: string) => {
    onPairChange(p);
    setOpen(false);
    setSearch('');
  };

  return (
    <div className="pair-picker">
      <button
        ref={triggerRef}
        type="button"
        className={`mstats-pair-btn ${open ? 'open' : ''}`}
        onClick={() => setOpen(v => !v)}
      >
        <div className="mstats-pair-main">
          <span className="mstats-asset">{activeBase}</span>
          <span className="mstats-slash">/</span>
          <span className="mstats-quote">{activeQuote}</span>
          <span className="mstats-caret">▾</span>
        </div>
        <div className="mstats-pair-sub">SPOT · FLARE EXCHANGE</div>
      </button>

      {open && ddPos && (
        <div
          ref={ddRef}
          className="pair-dd"
          style={{ top: ddPos.top, left: ddPos.left }}
        >
          <div className="pair-dd-search">
            <span className="dim">SEARCH</span>
            <input
              autoFocus
              placeholder="BTC, ETH, FLR…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <div className="pair-dd-head">
            <span>PAIR</span>
            <span className="r">LAST</span>
            <span className="r">24H</span>
          </div>
          <div className="pair-dd-list">
            {filtered.length === 0 && (
              <div className="pair-dd-empty">NO PAIRS MATCH</div>
            )}
            {filtered.map((p) => {
              const [base, quote] = p.split('/');
              const s = stats[p];
              const last = s?.lastPrice ?? 0;
              const change = s?.change24hPct ?? 0;
              const changeCls = change > 0 ? 'bid' : change < 0 ? 'ask' : 'dim';
              return (
                <button
                  key={p}
                  type="button"
                  className={`pair-dd-row ${p === pair ? 'active' : ''}`}
                  onClick={() => handleSelect(p)}
                >
                  <span className="pair-dd-pair">
                    <span className="pair-dd-base">{base}</span>
                    <span className="dim">/</span>
                    <span className="dim">{quote}</span>
                    <span className="pair-dd-name dim">{displayName(base, quote)}</span>
                  </span>
                  <span className="r">{fmtPrice(last)}</span>
                  <span className={`r ${changeCls}`}>{fmtChange(change)}</span>
                </button>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
