/**
 * withdraw.ts — mirrors tools/cmd/test-withdraw 2-step flow.
 *
 * Steps:
 * 1. Call withdraw(token, amount, to) on InstructionSender
 * 2. Poll proxy for TEE-signed result
 * 3. Call executeWithdrawal(token, amount, to, withdrawalId, signature)
 */

import type { Address, Hex } from "viem";
import { veilInstructionSenderAbi } from "../abi/veilInstructionSender";
import { pollResult } from "./teeClient";
import { INSTRUCTION_FEE } from "./deposit";

export interface WithdrawResp {
  token: string;
  amount: number;
  to: string;
  withdrawalId: string;
  signature: string;
  available: number;
}

export interface WithdrawConfig {
  writeContractAsync: (args: {
    address: Address;
    abi: readonly unknown[];
    functionName: string;
    args: unknown[];
    value?: bigint;
  }) => Promise<`0x${string}`>;
  waitForTransactionReceipt: (args: {
    hash: `0x${string}`;
  }) => Promise<{ status: string; logs: readonly { topics: readonly string[]; data: string }[] }>;
  instructionSender: Address;
  token: Address;
  amount: bigint;
  to: Address;
  proxyUrl: string;
}

export interface WithdrawResult {
  /** The TEE-signed withdrawal params (for retry if step 2 fails). */
  withdrawResp: WithdrawResp;
  /** Final executeWithdrawal tx hash. */
  executeTxHash: `0x${string}`;
}

/**
 * Execute the full 2-step withdrawal.
 *
 * onProgress is called with step descriptions for UI feedback.
 */
export async function executeWithdraw(
  config: WithdrawConfig,
  onProgress?: (step: string) => void
): Promise<WithdrawResult> {
  const {
    writeContractAsync,
    waitForTransactionReceipt,
    instructionSender,
    token,
    amount,
    to,
  } = config;

  // Step 1: Send withdraw instruction on-chain.
  onProgress?.("Sending withdraw transaction...");
  const withdrawTx = await writeContractAsync({
    address: instructionSender,
    abi: veilInstructionSenderAbi,
    functionName: "withdraw",
    args: [token, amount, to],
    value: INSTRUCTION_FEE,
  });
  await waitForTransactionReceipt({ hash: withdrawTx });

  // The proxy indexes by instruction ID which is embedded in the receipt logs.
  // For simplicity, we use the tx hash as the action ID to poll.
  const actionId = withdrawTx;

  // Step 2: Poll proxy for TEE-signed result.
  onProgress?.("Waiting for TEE signature...");
  const actionResult = await pollResult(actionId, 30, 2000);

  if (actionResult.result.status !== 1) {
    throw new Error(
      `Withdrawal instruction failed: ${actionResult.result.log}`
    );
  }

  const withdrawResp: WithdrawResp = JSON.parse(actionResult.result.data);

  // Step 3: Execute withdrawal on-chain with TEE signature.
  onProgress?.("Executing withdrawal on-chain...");
  const executeTx = await writeContractAsync({
    address: instructionSender,
    abi: veilInstructionSenderAbi,
    functionName: "executeWithdrawal",
    args: [
      withdrawResp.token as Address,
      BigInt(withdrawResp.amount),
      withdrawResp.to as Address,
      withdrawResp.withdrawalId as Hex,
      withdrawResp.signature as Hex,
    ],
  });
  await waitForTransactionReceipt({ hash: executeTx });

  return { withdrawResp, executeTxHash: executeTx };
}

/**
 * Execute just step 3 (retry) if the user already has a signed WithdrawResp.
 */
export async function executeWithdrawalRetry(
  config: Pick<WithdrawConfig, "writeContractAsync" | "waitForTransactionReceipt" | "instructionSender">,
  withdrawResp: WithdrawResp
): Promise<`0x${string}`> {
  const tx = await config.writeContractAsync({
    address: config.instructionSender,
    abi: veilInstructionSenderAbi,
    functionName: "executeWithdrawal",
    args: [
      withdrawResp.token as Address,
      BigInt(withdrawResp.amount),
      withdrawResp.to as Address,
      withdrawResp.withdrawalId as Hex,
      withdrawResp.signature as Hex,
    ],
  });
  await config.waitForTransactionReceipt({ hash: tx });
  return tx;
}
