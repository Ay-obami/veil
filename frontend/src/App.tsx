import "@rainbow-me/rainbowkit/styles.css";

import { getDefaultConfig, RainbowKitProvider, darkTheme } from "@rainbow-me/rainbowkit";
import { WagmiProvider } from "wagmi";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { coston2 } from "./config/chain";
import { env } from "./config/env";
import { ToastProvider } from "./components/ui/Toast";
import { TrayProvider } from "./components/ui/ActionTray";
import { Trade } from "./pages/Trade";

const config = getDefaultConfig({
  appName: "FLARE",
  projectId: env.walletConnectProjectId || "placeholder-project-id",
  chains: [coston2],
});

const queryClient = new QueryClient();

export default function App() {
  return (
    <WagmiProvider config={config}>
      <QueryClientProvider client={queryClient}>
        <RainbowKitProvider theme={darkTheme({ accentColor: '#c96aa8', accentColorForeground: '#0a0a0a', borderRadius: 'none', fontStack: 'system' })}>
          <ToastProvider>
            <TrayProvider>
              <Trade />
            </TrayProvider>
          </ToastProvider>
        </RainbowKitProvider>
      </QueryClientProvider>
    </WagmiProvider>
  );
}
