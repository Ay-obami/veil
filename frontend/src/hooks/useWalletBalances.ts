import { useAccount, useReadContracts } from "wagmi";
import { useQueryClient } from "@tanstack/react-query";
import type { Address } from "viem";
import { erc20Abi } from "../abi/erc20";
import { PAIRS } from "../config/generated";

export interface TokenInfo {
  balance: bigint | undefined;
  decimals: number | undefined;
}

function uniqueTokens(): Address[] {
  const tokens: Address[] = [];
  for (const pair of PAIRS) {
    const base = pair.baseToken.toLowerCase() as Address;
    const quote = pair.quoteToken.toLowerCase() as Address;
    if (!tokens.includes(base)) tokens.push(base);
    if (!tokens.includes(quote)) tokens.push(quote);
  }
  return tokens;
}

/**
 * Reads ERC20 balanceOf + decimals for every unique token across PAIRS.
 *
 * Decimals are split into their own query with infinite stale time because they
 * never change for an ERC20; balances poll separately. This way a flaky balance
 * refetch doesn't drop decimals and break formatting downstream.
 */
export function useWalletBalances() {
  const { address } = useAccount();
  const queryClient = useQueryClient();
  const tokens = uniqueTokens();

  const decimalsQuery = useReadContracts({
    contracts: tokens.map((token) => ({
      address: token,
      abi: erc20Abi,
      functionName: "decimals" as const,
    })),
    query: {
      enabled: tokens.length > 0,
      staleTime: Infinity,
      gcTime: Infinity,
    },
  });

  const balanceQuery = useReadContracts({
    contracts: tokens.map((token) => ({
      address: token,
      abi: erc20Abi,
      functionName: "balanceOf" as const,
      args: [address!] as const,
    })),
    query: {
      enabled: !!address && tokens.length > 0,
      refetchInterval: 5000,
    },
  });

  const tokenInfo: Record<string, TokenInfo> = {};
  tokens.forEach((token, i) => {
    const decResult = decimalsQuery.data?.[i];
    const balResult = balanceQuery.data?.[i];
    tokenInfo[token] = {
      decimals:
        decResult?.status === "success" ? Number(decResult.result) : undefined,
      balance:
        balResult?.status === "success" ? (balResult.result as bigint) : undefined,
    };
  });

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["readContracts"] });

  return {
    tokenInfo,
    isLoading: decimalsQuery.isLoading || balanceQuery.isLoading,
    invalidate,
  };
}
