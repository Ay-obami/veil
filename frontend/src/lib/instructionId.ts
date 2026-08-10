import type { Log } from "viem";

/**
 * Find the instruction ID from a deposit/withdraw tx receipt.
 *
 * The TeeExtensionRegistry emits `TeeInstructionsSent(uint256 indexed extensionId,
 * bytes32 indexed instructionId, uint32 indexed rewardEpochId, ...)`. We locate
 * that log by shape (4 topics) and return topic[2] = instructionId.
 *
 * The InstructionSender only emits this one event per deposit/withdraw call, so
 * matching on shape is reliable here without needing the registry's address or
 * full ABI.
 */
export function findInstructionId(logs: readonly Log[]): `0x${string}` | null {
  for (const log of logs) {
    if (log.topics.length >= 4 && log.topics[2]) {
      return log.topics[2] as `0x${string}`;
    }
  }
  return null;
}
