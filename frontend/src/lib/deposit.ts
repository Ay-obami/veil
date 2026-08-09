/**
 * deposit.ts — mirrors tools/cmd/test-deposit flow using viem/wagmi.
 *
 * Steps:
 * 1. Approve InstructionSender to spend tokens
 * 2. Call deposit(token, amount) on InstructionSender (with msg.value for fee)
 */

import type { Address } from "viem";
import { erc20Abi } from "../abi/erc20";
import { veilInstructionSenderAbi } from "../abi/veilInstructionSender";

/** Instruction fee in wei — matches the Go tooling constant. */
export const INSTRUCTION_FEE = BigInt(1_000_000);

export interface DepositConfig {
  writeContractAsync: (args: {
    address: Address;
    abi: readonly unknown[];
    functionName: string;
    args: unknown[];
    value?: bigint;
  }) => Promise<`0x${string}`>;
  waitForTransactionReceipt: (args: {
    hash: `0x${string}`;
  }) => Promise<{ status: string }>;
  instructionSender: Address;
  token: Address;
  amount: bigint;
}

/** Execute the approve + deposit flow. Returns the deposit tx hash. */
export async function executeDeposit(config: DepositConfig): Promise<`0x${string}`> {
  const { writeContractAsync, waitForTransactionReceipt, instructionSender, token, amount } = config;

  // Step 1: Approve
  const approveTx = await writeContractAsync({
    address: token,
    abi: erc20Abi,
    functionName: "approve",
    args: [instructionSender, amount],
  });
  await waitForTransactionReceipt({ hash: approveTx });

  // Step 2: Deposit
  const depositTx = await writeContractAsync({
    address: instructionSender,
    abi: veilInstructionSenderAbi,
    functionName: "deposit",
    args: [token, amount],
    value: INSTRUCTION_FEE,
  });
  await waitForTransactionReceipt({ hash: depositTx });

  return depositTx;
}
