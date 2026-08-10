import { formatUnits } from 'viem';
import { useMyState } from '../hooks/useMyState';
import { useWalletBalances } from '../hooks/useWalletBalances';
import { PAIRS } from '../config/generated';

function getUniqueTokens() {
  const tokens = new Map<string, string>(); // address → symbol derived from pair name
  for (const p of PAIRS) {
    const [base, quote] = p.name.split('/');
    tokens.set(p.baseToken.toLowerCase(), base);
    tokens.set(p.quoteToken.toLowerCase(), quote);
  }
  return Array.from(tokens.entries()).map(([address, symbol]) => ({ address, symbol }));
}

function fmtAmount(raw: number | bigint | undefined, decimals: number | undefined, dp = 4): string {
  if (raw === undefined || raw === null) return '—';
  if (decimals === undefined) return '—';
  try {
    const val = typeof raw === 'bigint' ? raw : BigInt(Math.floor(Number(raw)));
    return parseFloat(formatUnits(val, decimals)).toFixed(dp);
  } catch {
    return '—';
  }
}

export function Balances() {
  const { balances } = useMyState();
  const { tokenInfo } = useWalletBalances();
  const tokens = getUniqueTokens();

  // Normalize teeBalances keys to lowercase so lookups work regardless of
  // whether the TEE returns checksummed or lowercase addresses.
  const normalizedBalances: typeof balances = {};
  for (const [k, v] of Object.entries(balances)) {
    normalizedBalances[k.toLowerCase()] = v;
  }

  // Compute grand total as sum of all human-readable amounts for the alloc bar
  const totals = tokens.map(t => {
    const info = tokenInfo[t.address];
    const decimals = info?.decimals;
    const wallet = info?.balance !== undefined && decimals !== undefined
      ? parseFloat(formatUnits(info.balance, decimals))
      : 0;
    const tee = normalizedBalances[t.address];
    const available = tee && decimals !== undefined
      ? parseFloat(formatUnits(BigInt(Math.floor(tee.available)), decimals))
      : 0;
    const held = tee && decimals !== undefined
      ? parseFloat(formatUnits(BigInt(Math.floor(tee.held)), decimals))
      : 0;
    return wallet + available + held;
  });
  const grandTotal = totals.reduce((s, v) => s + v, 0);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div className="bal-total">
        <span className="label">PORTFOLIO</span>
        <span className="value">{grandTotal.toFixed(2)}</span>
      </div>
      <div style={{ flex: 1, overflow: 'auto' }}>
        <table className="tbl">
          <thead>
            <tr>
              <th>ASSET</th>
              <th className="num">WALLET</th>
              <th className="num">TEE AVAIL</th>
              <th className="num">TEE HELD</th>
              <th>ALLOC</th>
            </tr>
          </thead>
          <tbody>
            {tokens.map((t, i) => {
              const info = tokenInfo[t.address];
              const decimals = info?.decimals;
              const walletBal = info?.balance;
              const tee = normalizedBalances[t.address];
              const allocPct = grandTotal > 0 ? Math.min((totals[i] / grandTotal) * 100, 100) : 0;

              return (
                <tr key={t.address}>
                  <td style={{ fontWeight: 600, letterSpacing: '0.04em' }}>{t.symbol}</td>
                  <td className="num">
                    {fmtAmount(walletBal, decimals)}
                  </td>
                  <td className="num">
                    {tee ? fmtAmount(tee.available, decimals) : '—'}
                  </td>
                  <td className="num dim">
                    {tee ? fmtAmount(tee.held, decimals) : '—'}
                  </td>
                  <td>
                    <div className="alloc-bar">
                      <div style={{ width: `${allocPct}%` }} />
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
