import { useQuery } from "@tanstack/react-query";
import { getBookState, type BookStateResp, type PairState } from "../lib/orderbook";

/** Polls GET_BOOK_STATE for the given pair every 2s. Matches are newest-first and scoped to `pair`. */
export function useBookState(pair: string) {
  const query = useQuery<BookStateResp>({
    queryKey: ["bookState", pair],
    queryFn: () => getBookState({ pair }),
    refetchInterval: 2000,
  });

  const pairState: PairState | undefined = query.data?.state?.pairs?.[pair];
  const matches = query.data?.state?.matches ?? [];

  return {
    ...query,
    bids: pairState?.bids ?? [],
    asks: pairState?.asks ?? [],
    matches, // newest-first
  };
}
