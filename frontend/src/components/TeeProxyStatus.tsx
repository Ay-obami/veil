import { useQuery } from "@tanstack/react-query";
import { env } from "../config/env";

/**
 * Lightweight liveness probe against the TEE proxy. Detects when the proxy is
 * unreachable (no server at all, connection refused, unsafe-port, CORS, etc.)
 * and shows an actionable banner so dev setup issues are visible rather than
 * silent.
 */
async function probe(): Promise<"ok" | "fail"> {
  const url = `${env.teeProxyUrl || ""}/action/result/0x0000000000000000000000000000000000000000000000000000000000000000?submissionTag=submit`;
  try {
    // Any response (including 404/500) proves the proxy is reachable. We only
    // care about network/browser-level failures here.
    const res = await fetch(url, { method: "GET" });
    return res.status >= 0 ? "ok" : "fail";
  } catch {
    return "fail";
  }
}

export function TeeProxyStatus() {
  const { data } = useQuery({
    queryKey: ["teeProxyHealth"],
    queryFn: probe,
    refetchInterval: 5000,
    retry: false,
  });

  if (data !== "fail") return null;

  const configured = env.teeProxyUrl || "(relative — via vite dev proxy)";

  return (
    <div className="bg-red-900/40 border-b border-red-700 px-4 py-2 text-xs text-red-200">
      <span className="font-semibold">TEE proxy unreachable.</span>{" "}
      Balances, order book, and trades will not update until this is fixed.
      Configured URL: <code className="text-red-100">{configured}</code>.
      {" "}
      Likely causes: the TEE proxy isn't running, the cors-proxy (required for
      non-vite deployments) isn't running, or <code>VITE_TEE_PROXY_URL</code> /
      {" "}
      <code>VITE_PROXY_UPSTREAM</code> points at the wrong port. See README
      "Running the frontend".
    </div>
  );
}
