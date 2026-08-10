import { useQueries } from '@tanstack/react-query';
import { getCandles, type CandlesResp } from '../lib/orderbook';
import { formatPrice } from '../lib/price';
import { PAIRS } from '../config/generated';

export interface PairStats {
  lastPrice: number;
  change24hPct: number;
}

/**
 * Per-pair last price + 24h change derived from server-side 1h candles.
 * One GET_CANDLES poll per pair, run in parallel via useQueries.
 */
export function useAllPairStats(): Record<string, PairStats> {
  const results = useQueries({
    queries: PAIRS.map((p) => ({
      queryKey: ['candles', p.name, '1h', 24],
      queryFn: () => getCandles({ pair: p.name, timeframe: '1h', limit: 24 }),
      refetchInterval: 5000,
    })),
  });

  const stats: Record<string, PairStats> = {};
  for (let i = 0; i < PAIRS.length; i++) {
    const pair = PAIRS[i].name;
    const data = results[i].data as CandlesResp | undefined;
    const candles = data?.candles ?? [];
    if (candles.length === 0) continue;
    const last = formatPrice(candles[candles.length - 1].close);
    const ref = formatPrice(candles[0].open);
    const change = ref > 0 ? ((last - ref) / ref) * 100 : 0;
    stats[pair] = { lastPrice: last, change24hPct: change };
  }
  return stats;
}
