import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useAccount } from "wagmi";
import { cancelOrder, type CancelOrderResp } from "../lib/orderbook";
import type { StepReporter } from "../components/ui/ActionTray";

/** Tray step labels, in the order this hook advances through them. */
export const CANCEL_ORDER_STEPS = ["Submit to TEE", "TEE execution"];

interface CancelOrderArgs {
  orderId: string;
  report?: StepReporter;
}

export function useCancelOrder() {
  const { address } = useAccount();
  const queryClient = useQueryClient();

  return useMutation<CancelOrderResp, Error, CancelOrderArgs>({
    mutationFn: ({ orderId, report }) =>
      cancelOrder(
        { sender: address!.toLowerCase(), orderId },
        {
          onSubmitted: () => report?.advance(),
          onPoll: (n, max) => report?.detail(`attempt ${n}/${max}`),
        },
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["myState"] });
      queryClient.invalidateQueries({ queryKey: ["bookState"] });
    },
  });
}
