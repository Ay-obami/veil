/** Typed access to VITE_* env vars with defaults. */

export const env = {
  /** Base URL of the TEE proxy (cors-proxy in dev). */
  teeProxyUrl: import.meta.env.VITE_TEE_PROXY_URL as string || "",
  /** API key for the /direct endpoint. */
  directApiKey: import.meta.env.VITE_DIRECT_API_KEY as string || "",
  /** Show faucet button. */
  showFaucet: (import.meta.env.VITE_SHOW_FAUCET as string) !== "false",
  /** WalletConnect project ID. */
  walletConnectProjectId: import.meta.env.VITE_WALLETCONNECT_PROJECT_ID as string || "",
  /** Override for the deployed VeilInstructionSender contract address.
   *  Falls back to the value baked into generated.ts by sync-config. */
  instructionSender: import.meta.env.VITE_INSTRUCTION_SENDER as string || "",
} as const;
