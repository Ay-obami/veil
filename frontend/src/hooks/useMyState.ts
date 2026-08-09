import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useAccount } from "wagmi";
import { getMyState, type GetMyStateResp } from "../lib/orderbook";

/** Polls GET_MY_STATE every 3s for the connected wallet. */
export function useMyState() {
  const { address } = useAccount();
  const queryClient = useQueryClient();

  const query = useQuery<GetMyStateResp>({
    queryKey: ["myState", address],
    queryFn: async () => {
      const sender = address!.toLowerCase();
      console.log("[useMyState] fetching GET_MY_STATE", { sender });
      try {
        const resp = await getMyState(sender);
        console.log("[useMyState] response", {
          sender,
          balanceKeys: Object.keys(resp.balances ?? {}),
          balances: resp.balances,
          openOrdersCount: resp.openOrders?.length ?? 0,
          matchesCount: resp.matches?.length ?? 0,
        });
        return resp;
      } catch (err) {
        console.error("[useMyState] GET_MY_STATE failed", err);
        throw err;
      }
    },
    enabled: !!address,
    refetchInterval: 3000,
    retry: false,
  });

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["myState", address] });

  return {
    ...query,
    balances: query.data?.balances ?? {},
    openOrders: query.data?.openOrders ?? [],
    matches: query.data?.matches ?? [],
    invalidate,
  };
}
