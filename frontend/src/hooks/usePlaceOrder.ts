import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useAccount } from "wagmi";
import { placeOrder, getSolvencyCommitment, type PlaceOrderReq, type PlaceOrderResp } from "../lib/orderbook";
import { generateSolvencyProof, randomOrderNonce } from "../lib/solvencyProof";
import { useWalletBalances } from "./useWalletBalances";
import { scalePrice } from "../lib/price";
import { PAIRS } from "../config/generated";
import type { StepReporter } from "../components/ui/ActionTray";

/**
 * Tray step labels. The extra two only apply when `privateOrder` is set —
 * see PLACE_ORDER_STEPS_PRIVATE below. Kept separate rather than always
 * showing 4 steps so the common (non-private) path's UI doesn't grow an
 * extra beat for a feature most orders won't use.
 */
export const PLACE_ORDER_STEPS = ["Submit to TEE", "TEE execution"];
export const PLACE_ORDER_STEPS_PRIVATE = [
  "Request solvency commitment",
  "Generate ZK proof",
  "Submit to TEE",
  "TEE execution",
];

type PlaceOrderArgs = Omit<PlaceOrderReq, "sender" | "solvencyProof" | "solvencyPublicSignals"> & {
  report?: StepReporter;
  /**
   * When true, generates a ZK solvency proof and attaches it to the
   * order — see lib/solvencyProof.ts and internal/extension/solvency.go.
   * Requires the order to be a limit order (see that file's comment on
   * why market orders can't be proof-backed) and requires the relevant
   * balance (quote for buy, base for sell) to actually cover the order,
   * same as a normal order — the proof doesn't change what funds get
   * held, only what's publicly visible in the book (see
   * pkg/orderbook.OrderSide.Depth()'s doc comment).
   */
  privateOrder?: boolean;
};

export function usePlaceOrder() {
  const { address } = useAccount();
  const queryClient = useQueryClient();
  const { tokenInfo } = useWalletBalances();

  return useMutation<PlaceOrderResp, Error, PlaceOrderArgs>({
    mutationFn: async ({ report, privateOrder, ...req }) => {
      const pairConfig = PAIRS.find((p) => p.name === req.pair);
      if (!pairConfig) throw new Error(`Unknown pair: ${req.pair}`);

      const baseDecimals = tokenInfo[pairConfig.baseToken.toLowerCase()]?.decimals;
      const quoteDecimals = tokenInfo[pairConfig.quoteToken.toLowerCase()]?.decimals;

      if (baseDecimals === undefined || quoteDecimals === undefined) {
        throw new Error("Token decimals not yet loaded — try again in a moment");
      }

      // Quantity is in base-token human units; TEE expects raw integer units.
      const baseScale = Math.pow(10, baseDecimals);
      const scaledQuantity = Math.round(req.quantity * baseScale);
      // Price: scale by PRICE_PRECISION for 3 decimal places, then adjust for
      // any decimal difference between quote and base tokens (1x when equal).
      const quoteScale = Math.pow(10, quoteDecimals);
      const scaledPrice = scalePrice((req.price * quoteScale) / baseScale);

      let solvencyProof: PlaceOrderReq["solvencyProof"];
      let solvencyPublicSignals: PlaceOrderReq["solvencyPublicSignals"];

      if (privateOrder) {
        if (req.type !== "limit") {
          throw new Error("Private orders are only supported for limit orders");
        }
        // Mirrors calculateHold's determination (see
        // internal/extension/handlers.go): quote token + price*quantity
        // for a buy, base token + quantity for a sell. PRICE_PRECISION
        // (1e6) matches scalePrice's own scale — see lib/price.ts.
        const PRICE_PRECISION = 1_000_000;
        const holdToken = req.side === "buy" ? pairConfig.quoteToken : pairConfig.baseToken;
        const requiredAmount =
          req.side === "buy"
            ? Math.floor((scaledQuantity * scaledPrice) / PRICE_PRECISION)
            : scaledQuantity;

        const commitment = await getSolvencyCommitment({
          sender: address!.toLowerCase(),
          token: holdToken,
        });
        report?.advance(); // "Request solvency commitment" done

        const { proof, publicSignals } = await generateSolvencyProof({
          collateral: commitment.balance,
          threshold: requiredAmount,
          orderId: randomOrderNonce(),
          nonce: commitment.nonce,
          commitment: commitment.commitment,
        });
        report?.advance(); // "Generate ZK proof" done

        solvencyProof = proof;
        solvencyPublicSignals = publicSignals;
      }

      return placeOrder(
        {
          ...req,
          sender: address!.toLowerCase(),
          quantity: scaledQuantity,
          price: scaledPrice,
          solvencyProof,
          solvencyPublicSignals,
        },
        {
          onSubmitted: () => report?.advance(),
          onPoll: (n, max) => report?.detail(`attempt ${n}/${max}`),
        },
      );
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["myState"] });
      queryClient.invalidateQueries({ queryKey: ["bookState"] });
    },
  });
}
