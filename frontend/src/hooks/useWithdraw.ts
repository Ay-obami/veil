import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useWriteContract, usePublicClient } from "wagmi";
import type { Address } from "viem";
import { veilInstructionSenderAbi } from "../abi/veilInstructionSender";
import { INSTRUCTION_FEE } from "../lib/deposit";
import { pollResult, decodeResultData } from "../lib/teeClient";
import { findInstructionId } from "../lib/instructionId";
import type { WithdrawResp } from "../lib/withdraw";
import type { StepReporter } from "../components/ui/ActionTray";

interface WithdrawArgs {
  instructionSender: Address;
  token: Address;
  amount: bigint;
  to: Address;
  report?: StepReporter;
}

/** Tray step labels, in the order this hook advances through them. */
export const WITHDRAW_STEPS = [
  "Withdraw transaction",
  "TEE signature",
  "Execute on-chain",
];

export function useWithdraw() {
  const { writeContractAsync } = useWriteContract();
  const publicClient = usePublicClient();
  const queryClient = useQueryClient();
  const [cachedSignature, setCachedSignature] = useState<WithdrawResp | null>(null);

  /**
   * Step 3, shared by the main flow and the retry path. Caches the TEE
   * signature on failure so the user can re-run just this step — steps 1-2
   * cost a tx and up to a minute of polling, so redoing them is expensive.
   */
  async function execute(
    instructionSender: Address,
    wr: WithdrawResp,
    report?: StepReporter,
  ): Promise<`0x${string}`> {
    if (!publicClient) throw new Error("No public client");

    const args = [
      wr.token as Address,
      BigInt(wr.amount),
      wr.to as Address,
      wr.withdrawalId as `0x${string}`,
      wr.signature as `0x${string}`,
    ] as const;

    let tx: `0x${string}`;
    report?.detail("confirm in wallet");
    try {
      tx = await writeContractAsync({
        address: instructionSender,
        abi: veilInstructionSenderAbi,
        functionName: "executeWithdrawal",
        args: [...args],
      });
    } catch (e) {
      setCachedSignature(wr);
      throw e;
    }

    report?.detail("mining");
    const receipt = await publicClient.waitForTransactionReceipt({ hash: tx });
    if (receipt.status !== "success") {
      setCachedSignature(wr);
      throw new Error(`executeWithdrawal tx reverted (${tx})`);
    }

    setCachedSignature(null);
    return tx;
  }

  const mutation = useMutation<`0x${string}`, Error, WithdrawArgs>({
    mutationFn: async ({ instructionSender, token, amount, to, report }) => {
      if (!publicClient) throw new Error("No public client");

      // Step 1: Send withdraw instruction on-chain.
      report?.detail("confirm in wallet");
      const withdrawTx = await writeContractAsync({
        address: instructionSender,
        abi: veilInstructionSenderAbi,
        functionName: "withdraw",
        args: [token, amount, to],
        value: INSTRUCTION_FEE,
      });

      report?.detail("mining");
      const withdrawReceipt = await publicClient.waitForTransactionReceipt({ hash: withdrawTx });
      if (withdrawReceipt.status !== "success") {
        throw new Error(`Withdraw tx reverted (${withdrawTx})`);
      }

      const instructionId = findInstructionId(withdrawReceipt.logs);
      if (!instructionId) {
        throw new Error("Withdraw tx mined but no TeeInstructionsSent event found");
      }
      report?.advance();

      // Step 2: Poll proxy for TEE-signed result.
      // On-chain instructions are stored with submissionTag="threshold" (not "submit").
      const actionResult = await pollResult(
        instructionId,
        30,
        2000,
        "threshold",
        (n, max) => report?.detail(`attempt ${n}/${max}`),
      );

      if (actionResult.result.status !== 1) {
        throw new Error(`Withdrawal failed: ${actionResult.result.log}`);
      }

      const wr: WithdrawResp = decodeResultData<WithdrawResp>(actionResult.result.data);
      report?.advance();

      // Step 3: Execute withdrawal on-chain.
      return execute(instructionSender, wr, report);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["myState"] });
      queryClient.invalidateQueries({ queryKey: ["readContracts"] });
    },
  });

  /** Retry step 3 alone, using the signature cached when it failed. */
  const retryExecute = async (instructionSender: Address, report?: StepReporter) => {
    if (!cachedSignature) throw new Error("No cached signature");
    const tx = await execute(instructionSender, cachedSignature, report);
    queryClient.invalidateQueries({ queryKey: ["myState"] });
    queryClient.invalidateQueries({ queryKey: ["readContracts"] });
    return tx;
  };

  return { ...mutation, cachedSignature, retryExecute };
}
