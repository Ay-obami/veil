import { formatUnits } from 'viem';
import { useBookState } from '../hooks/useBookState';
import { useWalletBalances } from '../hooks/useWalletBalances';
import { PAIRS } from '../config/generated';
import { formatPriceAdaptive } from '../lib/price';

interface RecentTradesProps {
  pair: string;
}

function fmtTime(ts: number): string {
  if (!ts || ts <= 0) return '—';
  // normalize unix s / ms / ns → ms
  const ms = ts > 1e15 ? Math.floor(ts / 1e6) : ts > 1e12 ? ts : ts * 1000;
  const d = new Date(ms);
  if (isNaN(d.getTime())) return '—';
  return d.toLocaleTimeString('en-GB', { hour12: false });
}

export function RecentTrades({ pair }: RecentTradesProps) {
  const { matches } = useBookState(pair);
  const { tokenInfo } = useWalletBalances();
  const pairConfig = PAIRS.find(p => p.name === pair);
  const baseDecimals = pairConfig
    ? tokenInfo[pairConfig.baseToken.toLowerCase()]?.decimals
    : undefined;

  // matches arrive newest-first from the backend; cap at 40 for display.
  const recent = matches.slice(0, 40);

  function fmtQty(raw: number): string {
    if (baseDecimals === undefined) return String(raw);
    return parseFloat(formatUnits(BigInt(Math.floor(raw)), baseDecimals)).toFixed(4);
  }

  if (recent.length === 0) {
    return (
      <div className="tape">
        <div className="tape-head">
          <span>TIME</span>
          <span>PRICE</span>
          <span>SIZE</span>
        </div>
        <div className="tape-body">
          <div className="empty-hint">NO TRADES</div>
        </div>
      </div>
    );
  }

  return (
    <div className="tape">
      <div className="tape-head">
        <span>TIME</span>
        <span>PRICE</span>
        <span>SIZE</span>
      </div>
      <div className="tape-body">
        {recent.map((t, i) => {
          // Direction: compare with next-older trade (i+1 = older)
          const older = recent[i + 1];
          const dir = !older ? 'up' : t.price > older.price ? 'up' : t.price < older.price ? 'dn' : 'up';
          return (
            <div
              key={`${t.timestamp}-${t.buyOrderId}-${t.sellOrderId}-${i}`}
              className={`tape-row ${dir}`}
            >
              <span className="time">{fmtTime(t.timestamp)}</span>
              <span className="price">{dir === 'up' ? '▲' : '▼'} {formatPriceAdaptive(t.price)}</span>
              <span className="size">{fmtQty(t.quantity)}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
