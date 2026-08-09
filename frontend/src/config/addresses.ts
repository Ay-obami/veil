/**
 * Resolved contract addresses — prefers VITE_* env overrides, falls back to
 * the values synced from the parent repo into generated.ts. Needed once the
 * frontend is deployed separately from the extension repo (e.g. Vercel +
 * remote TEE proxy), where generated.ts may not reflect the live deployment.
 */

import { INSTRUCTION_SENDER as GENERATED_INSTRUCTION_SENDER } from "./generated";
import { env } from "./env";

export const INSTRUCTION_SENDER =
  (env.instructionSender || GENERATED_INSTRUCTION_SENDER) as `0x${string}`;
