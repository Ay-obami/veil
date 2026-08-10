/**
 * teeClient.ts — mirrors tools/pkg/utils/direct.go.
 *
 * postDirect() sends a direct instruction and returns the action ID.
 * pollResult() polls for the result until it resolves.
 */

import { env } from "../config/env";

/** The envelope the proxy returns from POST /direct. */
interface DirectResponse {
  data: { id: string };
}

/** The envelope returned from GET /action/result/:id. */
export interface ActionResult {
  result: {
    status: number; // 0 = failed, 1 = success, 2 = pending
    log: string;
    data: string; // JSON-encoded result data
  };
}

/**
 * Converts a string to a bytes32-style 0x hex (left-padded with zeros),
 * matching the Go `ToBytes32` / Solidity `bytes32("...")` convention.
 */
function toBytes32Hex(s: string): string {
  const hex = Array.from(new TextEncoder().encode(s))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  return "0x" + hex.padEnd(64, "0");
}

function baseUrl(): string {
  // In dev mode, Vite proxies /direct etc. to the cors-proxy.
  // In production, VITE_TEE_PROXY_URL is the full URL.
  return env.teeProxyUrl;
}

/**
 * Send a direct instruction to the proxy.
 * Returns the action ID (hex string).
 */
export async function postDirect(
  opCommand: string,
  payload: unknown
): Promise<string> {
  const msgJson = JSON.stringify(payload);
  // Convert message to hex-encoded bytes (like hexutil.Bytes in Go).
  const msgHex =
    "0x" +
    Array.from(new TextEncoder().encode(msgJson))
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");

  const body = {
    opType: toBytes32Hex("ORDERBOOK"),
    opCommand: toBytes32Hex(opCommand),
    message: msgHex,
  };

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (env.directApiKey) {
    headers["X-API-Key"] = env.directApiKey;
  }

  const res = await fetch(`${baseUrl()}/direct`, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });

  if (!res.ok) {
    const text = await res.text();
    throw new Error(`POST /direct returned ${res.status}: ${text}`);
  }

  const data: DirectResponse = await res.json();
  return data.data.id;
}

/**
 * Results are stored under different submission tags depending on how the
 * instruction was dispatched:
 *   - "submit"    → direct instructions (POST /direct)
 *   - "threshold" → on-chain instructions (TeeInstructionsSent events).
 *                   Later re-emitted under "end" once finalized — we poll
 *                   "threshold" because it's written first and both carry the
 *                   TEE's result data.
 *
 * The Go tooling uses two separate pollers for the same reason (see
 * tools/pkg/utils/direct.go:pollDirectResult and tools/pkg/fccutils/tee_calls.go:ActionResult).
 */
export type SubmissionTag = "submit" | "threshold" | "end";

/** Called before each poll attempt so callers can show progress. */
export type PollProgress = (attempt: number, maxAttempts: number) => void;

/**
 * Poll for an action result. Retries up to `maxAttempts` with `intervalMs` delay.
 * `submissionTag` defaults to "submit" (direct instructions); pass "threshold"
 * for on-chain instructions like deposits/withdrawals.
 */
export async function pollResult(
  actionId: string,
  maxAttempts = 15,
  intervalMs = 2000,
  submissionTag: SubmissionTag = "submit",
  onProgress?: PollProgress
): Promise<ActionResult> {
  const url = `${baseUrl()}/action/result/${actionId}?submissionTag=${submissionTag}`;

  for (let i = 0; i < maxAttempts; i++) {
    onProgress?.(i + 1, maxAttempts);
    try {
      const res = await fetch(url);
      if (res.ok) {
        return await res.json();
      }
    } catch {
      // Retry on network errors.
    }
    await new Promise((r) => setTimeout(r, intervalMs));
  }

  throw new Error(`Timed out polling for action ${actionId} (tag=${submissionTag})`);
}

/** Hex-decode result.data if it starts with 0x, then JSON.parse. */
export function decodeResultData<T>(data: string): T {
  let s = data;
  if (s.startsWith("0x")) {
    const hex = s.slice(2);
    const bytes = new Uint8Array(hex.match(/.{1,2}/g)!.map((b) => parseInt(b, 16)));
    s = new TextDecoder().decode(bytes);
  }
  return JSON.parse(s) as T;
}

export interface DirectProgress {
  /** The proxy accepted the instruction; polling for execution starts now. */
  onSubmitted?: (actionId: string) => void;
  onPoll?: PollProgress;
}

/**
 * Send a direct instruction and poll for the result.
 * Returns the parsed result data, or throws on failure.
 */
export async function sendDirectAndPoll<T>(
  opCommand: string,
  payload: unknown,
  progress?: DirectProgress
): Promise<T> {
  const actionId = await postDirect(opCommand, payload);
  progress?.onSubmitted?.(actionId);
  const actionResult = await pollResult(actionId, 15, 2000, "submit", progress?.onPoll);

  if (actionResult.result.status === 0) {
    throw new Error(`Instruction failed: ${actionResult.result.log}`);
  }
  if (actionResult.result.status === 2) {
    throw new Error(`Instruction still pending after polling (${actionId})`);
  }

  if (actionResult.result.data) {
    return decodeResultData<T>(actionResult.result.data);
  }
  return undefined as unknown as T;
}
