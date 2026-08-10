import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useWriteContract, usePublicClient, useAccount } from "wagmi";
import { type Address } from "viem";
import { erc20Abi } from "../abi/erc20";
import { veilInstructionSenderAbi } from "../abi/veilInstructionSender";
import { INSTRUCTION_FEE } from "../lib/deposit";
import { findInstructionId } from "../lib/instructionId";
import { pollResult } from "../lib/teeClient";
import type { StepReporter } from "../components/ui/ActionTray";

interface DepositArgs {
  instructionSender: Address;
  token: Address;
  amount: bigint;
  report?: StepReporter;
}

/** Tray step labels, in the order this hook advances through them. */
export function depositSteps(symbol: string): string[] {
  return [`Approve ${symbol}`, "Deposit transaction", "TEE confirmation"];
}

export function useDeposit() {
  const { writeContractAsync } = useWriteContract();
  const publicClient = usePublicClient();
  const { address } = useAccount();
  const queryClient = useQueryClient();

  return useMutation<`0x${string}`, Error, DepositArgs>({
    mutationFn: async ({ instructionSender, token, amount, report }) => {
      if (!publicClient) throw new Error("No public client");
      if (!address) throw new Error("Wallet not connected");

      // Step 0: KYC check (only if enabled on the contract).
      report?.detail("checking allowlist");
      const kycEnabled = await publicClient.readContract({
        address: instructionSender,
        abi: veilInstructionSenderAbi,
        functionName: "kycEnabled",
      });

      if (kycEnabled) {
        const isAllowed = await publicClient.readContract({
          address: instructionSender,
          abi: veilInstructionSenderAbi,
          functionName: "allowed",
          args: [address],
        });
        if (!isAllowed) {
          throw new Error(
            `KYC is enabled and wallet ${address} is not on the allowlist. ` +
              `An admin must call allowUser(${address}) on the InstructionSender.`,
          );
        }
      }

      // Step 1: Approve
      report?.detail("confirm in wallet");
      const approveTx = await writeContractAsync({
        address: token,
        abi: erc20Abi,
        functionName: "approve",
        args: [instructionSender, amount],
      });
      report?.detail("mining");
      const approveReceipt = await publicClient.waitForTransactionReceipt({ hash: approveTx });
      if (approveReceipt.status !== "success") {
        throw new Error(`Approve tx reverted (${approveTx})`);
      }
      report?.advance();

      // Step 2: Deposit (on-chain tx — also enqueues a DEPOSIT instruction for the TEE).
      report?.detail("confirm in wallet");
      const depositTx = await writeContractAsync({
        address: instructionSender,
        abi: veilInstructionSenderAbi,
        functionName: "deposit",
        args: [token, amount],
        value: INSTRUCTION_FEE,
      });
      report?.detail("mining");
      const depositReceipt = await publicClient.waitForTransactionReceipt({ hash: depositTx });
      if (depositReceipt.status !== "success") {
        throw new Error(`Deposit tx reverted (${depositTx})`);
      }
      report?.advance();

      // Step 3: Pull the instruction ID out of the TeeInstructionsSent event and
      // poll the proxy until the TEE has actually processed the deposit. Without
      // this wait, the tx succeeds but the TEE balance may not be credited yet.
      const instructionId = findInstructionId(depositReceipt.logs);
      if (!instructionId) {
        throw new Error(
          "Deposit tx mined but no TeeInstructionsSent event found — cannot confirm TEE processing.",
        );
      }

      // On-chain instructions are stored with submissionTag="threshold" (not "submit").
      const actionResult = await pollResult(
        instructionId,
        30,
        2000,
        "threshold",
        (n, max) => report?.detail(`attempt ${n}/${max}`),
      );
      if (actionResult.result.status === 0) {
        throw new Error(`TEE rejected deposit: ${actionResult.result.log}`);
      }
      if (actionResult.result.status === 2) {
        throw new Error(
          `TEE deposit still pending after polling (instruction ${instructionId})`,
        );
      }

      return depositTx;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["myState"] });
      queryClient.invalidateQueries({ queryKey: ["readContracts"] });
    },
  });
}
