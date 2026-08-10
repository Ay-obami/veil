import { formatUnits } from 'viem';
import { useAccount } from 'wagmi';
import { useMyState } from '../hooks/useMyState';
import { useWalletBalances } from '../hooks/useWalletBalances';
import { PAIRS } from '../config/generated';
import { formatPriceAdaptive } from '../lib/price';

interface MyFillsProps {
  pair: string;
}

function fmtTime(ts: number): string {
  if (!ts || ts <= 0) return '—';
  const ms = ts > 1e15 ? Math.floor(ts / 1e6) : ts > 1e12 ? ts : ts * 1000;
  const d = new Date(ms);
  if (isNaN(d.getTime())) return '—';
  return d.toLocaleTimeString('en-GB', { hour12: false });
}

export function MyFills({ pair }: MyFillsProps) {
  const { address } = useAccount();
  const { matches } = useMyState();
  const { tokenInfo } = useWalletBalances();

  const me = address?.toLowerCase() ?? '';
  const pairConfig = PAIRS.find(p => p.name === pair);
  const baseDecimals = pairConfig
    ? tokenInfo[pairConfig.baseToken.toLowerCase()]?.decimals
    : undefined;

  function fmtQty(raw: number): string {
    if (baseDecimals === undefined) return String(raw);
    return parseFloat(formatUnits(BigInt(Math.floor(raw)), baseDecimals)).toFixed(4);
  }

  const rows = [...matches]
    .filter(m => m.pair === pair)
    .reverse()
    .slice(0, 100);

  if (!rows.length) {
    return (
      <table className="tbl">
        <thead>
          <tr>
            <th>TIME</th>
            <th>SIDE</th>
            <th className="num">PRICE</th>
            <th className="num">SIZE</th>
          </tr>
        </thead>
        <tbody>
          <tr className="empty-row">
            <td colSpan={4}>NO FILLS</td>
          </tr>
        </tbody>
      </table>
    );
  }

  return (
    <table className="tbl">
      <thead>
        <tr>
          <th>TIME</th>
          <th>SIDE</th>
          <th className="num">PRICE</th>
          <th className="num">SIZE</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((m, i) => {
          const side = m.buyOwner.toLowerCase() === me ? 'buy' : 'sell';
          return (
            <tr key={`${m.timestamp}-${m.buyOrderId}-${m.sellOrderId}-${i}`}>
              <td className="num" style={{ color: 'var(--fg-mute)' }}>{fmtTime(m.timestamp)}</td>
              <td className={side === 'buy' ? 'bid' : 'ask'}>{side.toUpperCase()}</td>
              <td className="num">{formatPriceAdaptive(m.price)}</td>
              <td className="num">{fmtQty(m.quantity)}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
