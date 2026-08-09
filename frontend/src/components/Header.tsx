import { useState, useEffect } from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';

interface HeaderProps {
  hiddenPanels: { id: string; label: string }[];
  onRestore: (id: string) => void;
  bottomHidden: boolean;
  onRestoreBottom: () => void;
  onOpenWallet: () => void;
}

export function Header({ hiddenPanels, onRestore, bottomHidden, onRestoreBottom, onOpenWallet }: HeaderProps) {
  const [clock, setClock] = useState('');

  useEffect(() => {
    const tick = () => setClock(new Date().toLocaleTimeString('en-GB', { hour12: false, timeZone: 'UTC' }) + ' UTC');
    tick();
    const id = setInterval(tick, 1000);
    return () => clearInterval(id);
  }, []);

  const showChips = hiddenPanels.length > 0 || bottomHidden;

  return (
    <header className="hdr">
      <div className="hdr-brand">
        <span className="accent-text">◆</span>
        <span className="name">FLARE</span>
        <span className="sub">/ exchange</span>
      </div>

      <nav className="hdr-nav">
        <button className="active">SPOT</button>
        <button disabled>PERP</button>
        <button disabled>OPTIONS</button>
      </nav>

      {showChips && (
        <div className="hdr-chips">
          {hiddenPanels.map(p => (
            <button key={p.id} className="hdr-chip" onClick={() => onRestore(p.id)}>
              + {p.label}
            </button>
          ))}
          {bottomHidden && (
            <button className="hdr-chip" onClick={onRestoreBottom}>+ ACTIVITY</button>
          )}
        </div>
      )}

      <div className="hdr-right">
        <div className="hdr-live">
          <span className="dot" />
          <span>LIVE</span>
        </div>
        <span className="hdr-clock">{clock}</span>
        <button className="hdr-wallet-btn" onClick={onOpenWallet}>WALLET</button>
        <ConnectButton.Custom>
          {({ account, chain, openConnectModal, openAccountModal, mounted }) => {
            if (!mounted) return null;
            if (!account || !chain) {
              return (
                <button className="hdr-user" onClick={openConnectModal}>
                  CONNECT
                </button>
              );
            }
            return (
              <button className="hdr-user" onClick={openAccountModal}>
                {account.displayName}
              </button>
            );
          }}
        </ConnectButton.Custom>
      </div>
    </header>
  );
}
