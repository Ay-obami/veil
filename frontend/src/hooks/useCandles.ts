import { useQuery } from "@tanstack/react-query";
import { getCandles, type CandlesResp } from "../lib/orderbook";
import type { Timeframe } from "../lib/candles";

/** Polls GET_CANDLES for (pair, timeframe). Server returns oldest-first OHLCV. */
export function useCandles(pair: string, timeframe: Timeframe, limit = 240) {
  return useQuery<CandlesResp>({
    queryKey: ["candles", pair, timeframe, limit],
    queryFn: () => getCandles({ pair, timeframe, limit }),
    refetchInterval: 2000,
  });
}
