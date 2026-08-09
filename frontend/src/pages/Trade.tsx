import { useState, useEffect, useRef, useCallback } from 'react';
import { Header } from '../components/Header';
import { MarketStats } from '../components/MarketStats';
import { Chart } from '../components/Chart';
import { OrderBook } from '../components/OrderBook';
import { OrderForm } from '../components/OrderForm';
import { RecentTrades } from '../components/RecentTrades';
import { MyFills } from '../components/MyFills';
import { Balances } from '../components/Balances';
import { OpenOrders } from '../components/OpenOrders';
import { WalletModal } from '../components/WalletModal';
import { TeeProxyStatus } from '../components/TeeProxyStatus';
import { Panel } from '../components/ui/Panel';
import { useBookState } from '../hooks/useBookState';
import { useWalletBalances } from '../hooks/useWalletBalances';
import { PAIRS } from '../config/generated';
import { TIMEFRAMES, type Timeframe } from '../lib/candles';

// ─── Layout state ─────────────────────────────────────────────

const LS_KEY = 'ledger:layout:v2';
const TF_LS_KEY = 'ledger:chart:timeframe';
const PAIR_LS_KEY = 'ledger:pair';

function loadTimeframe(): Timeframe {
  try {
    const raw = localStorage.getItem(TF_LS_KEY);
    if (raw && (TIMEFRAMES as readonly string[]).includes(raw)) return raw as Timeframe;
  } catch {}
  return '15m';
}

function loadPair(): string {
  const names = PAIRS.map(p => p.name);
  const fallback = names[0] ?? 'FLR/USDT';
  try {
    const raw = localStorage.getItem(PAIR_LS_KEY);
    if (raw && names.includes(raw)) return raw;
  } catch {}
  return fallback;
}

interface PanelVis {
  chart: boolean;
  book: boolean;
  entry: boolean;
  tape: boolean;
}

interface LayoutState {
  panels: PanelVis;
  topFlex: { chart: number; book: number; entry: number; tape: number };
  bottomVisible: boolean;
  bottomHeight: number;
}

const DEFAULT_LAYOUT: LayoutState = {
  panels: { chart: true, book: true, entry: true, tape: true },
  topFlex: { chart: 1.5, book: 1.4, entry: 0.9, tape: 0.7 },
  bottomVisible: true,
  bottomHeight: 280,
};

function loadLayout(): LayoutState {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (!raw) return { ...DEFAULT_LAYOUT };
    const saved = JSON.parse(raw);
    return {
      panels: { ...DEFAULT_LAYOUT.panels, ...(saved.panels ?? {}) },
      topFlex: { ...DEFAULT_LAYOUT.topFlex, ...(saved.topFlex ?? {}) },
      bottomVisible: saved.bottomVisible ?? DEFAULT_LAYOUT.bottomVisible,
      bottomHeight: saved.bottomHeight ?? DEFAULT_LAYOUT.bottomHeight,
    };
  } catch {
    return { ...DEFAULT_LAYOUT };
  }
}

// ─── Resizable row ─────────────────────────────────────────────

interface RowPanel {
  id: keyof PanelVis;
  label: string;
  flex: number;
  minWidth: number;
  node: React.ReactNode;
}

function ResizableRow({
  panels,
  onResizePair,
}: {
  panels: RowPanel[];
  onResizePair: (idA: keyof PanelVis, flexA: number, idB: keyof PanelVis, flexB: number) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);

  const startDrag = useCallback(
    (i: number, e: React.MouseEvent) => {
      e.preventDefault();
      const cont = containerRef.current;
      if (!cont || i >= panels.length - 1) return;

      const rect = cont.getBoundingClientRect();
      // Total width minus handle widths (4px each)
      const handleCount = panels.length - 1;
      const totalWidth = rect.width - handleCount * 4;
      const totalFlex = panels.reduce((s, p) => s + p.flex, 0);

      const a = panels[i];
      const b = panels[i + 1];
      const sumFlex = a.flex + b.flex;
      const sumWidth = (sumFlex / totalFlex) * totalWidth;

      // Find left edge of panel a
      const panelEls = cont.querySelectorAll<HTMLDivElement>('[data-panel-flex]');
      const aLeft = panelEls[i]?.getBoundingClientRect().left ?? rect.left;

      const onMove = (ev: MouseEvent) => {
        let aw = ev.clientX - aLeft;
        aw = Math.max(a.minWidth, Math.min(sumWidth - b.minWidth, aw));
        const aFlex = (aw / sumWidth) * sumFlex;
        const bFlex = sumFlex - aFlex;
        onResizePair(a.id, aFlex, b.id, bFlex);
      };

      const onUp = () => {
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
      };

      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
    },
    [panels, onResizePair]
  );

  if (panels.length === 0) {
    return (
      <div style={{ display: 'flex', flex: 1, alignItems: 'center', justifyContent: 'center', background: 'var(--bg)' }}>
        <div className="empty-hint">ALL PANELS HIDDEN · restore from the header</div>
      </div>
    );
  }

  return (
    <div ref={containerRef} style={{ display: 'flex', flex: 1, minHeight: 0, overflow: 'hidden' }}>
      {panels.map((p, i) => (
        <div key={p.id} style={{ display: 'flex', flexDirection: 'row', flex: `${p.flex}`, minWidth: p.minWidth, minHeight: 0, overflow: 'hidden' }}>
          <div data-panel-flex="true" style={{ flex: 1, minWidth: 0, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
            {p.node}
          </div>
          {i < panels.length - 1 && (
            <div
              className="h-resizer"
              onMouseDown={(e) => startDrag(i, e)}
            />
          )}
        </div>
      ))}
    </div>
  );
}

// ─── Main Trade component ──────────────────────────────────────

export function Trade() {
  const [pair, setPair] = useState<string>(loadPair);

  const [prefillPrice, setPrefillPrice] = useState<number | null>(null);
  const [walletOpen, setWalletOpen] = useState(false);
  const [activityTab, setActivityTab] = useState<'OPEN ORDERS' | 'FILLS' | 'BALANCES'>('OPEN ORDERS');
  const [layout, setLayout] = useState<LayoutState>(loadLayout);
  const [timeframe, setTimeframe] = useState<Timeframe>(loadTimeframe);

  // Persist layout
  useEffect(() => {
    try { localStorage.setItem(LS_KEY, JSON.stringify(layout)); } catch {}
  }, [layout]);

  useEffect(() => {
    try { localStorage.setItem(TF_LS_KEY, timeframe); } catch {}
  }, [timeframe]);

  useEffect(() => {
    try { localStorage.setItem(PAIR_LS_KEY, pair); } catch {}
    setPrefillPrice(null);
  }, [pair]);

  const { bids, asks, matches } = useBookState(pair);
  const { tokenInfo } = useWalletBalances();
  const pairConfig = PAIRS.find(p => p.name === pair);
  const baseDecimals = pairConfig
    ? tokenInfo[pairConfig.baseToken.toLowerCase()]?.decimals
    : undefined;

  // Panel toggle helpers
  const togglePanel = useCallback((id: keyof PanelVis) => {
    setLayout(l => ({ ...l, panels: { ...l.panels, [id]: !l.panels[id] } }));
  }, []);

  const setTopFlex = useCallback((idA: keyof PanelVis, flexA: number, idB: keyof PanelVis, flexB: number) => {
    setLayout(l => ({
      ...l,
      topFlex: { ...l.topFlex, [idA]: flexA, [idB]: flexB },
    }));
  }, []);

  // Bottom resize
  const bottomDragRef = useRef<{ startY: number; startH: number } | null>(null);
  const startBottomResize = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    bottomDragRef.current = { startY: e.clientY, startH: layout.bottomHeight };
    document.body.style.cursor = 'row-resize';
    document.body.style.userSelect = 'none';

    const onMove = (ev: MouseEvent) => {
      const d = bottomDragRef.current;
      if (!d) return;
      const dy = d.startY - ev.clientY; // drag up = bigger bottom
      const h = Math.max(140, Math.min(window.innerHeight * 0.65, d.startH + dy));
      setLayout(l => ({ ...l, bottomHeight: h }));
    };
    const onUp = () => {
      bottomDragRef.current = null;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, [layout.bottomHeight]);

  // Build hidden panels list for header chips
  const hiddenPanels: { id: string; label: string }[] = [];
  if (!layout.panels.chart) hiddenPanels.push({ id: 'chart', label: 'CHART' });
  if (!layout.panels.book) hiddenPanels.push({ id: 'book', label: 'BOOK' });
  if (!layout.panels.entry) hiddenPanels.push({ id: 'entry', label: 'ENTRY' });
  if (!layout.panels.tape) hiddenPanels.push({ id: 'tape', label: 'TAPE' });

  // Build visible top panels
  const topPanels: RowPanel[] = [];
  if (layout.panels.chart) {
    topPanels.push({
      id: 'chart',
      label: 'CHART',
      flex: layout.topFlex.chart,
      minWidth: 280,
      node: (
        <Panel
          id="chart"
          title={`CHART · ${pair} · LIVE`}
          right={
            <div className="chart-tfs">
              {TIMEFRAMES.map((tf) => (
                <button
                  key={tf}
                  type="button"
                  className={timeframe === tf ? 'active' : ''}
                  onClick={() => setTimeframe(tf)}
                >
                  {tf}
                </button>
              ))}
            </div>
          }
          noPad
          onClose={() => togglePanel('chart')}
        >
          <Chart pair={pair} timeframe={timeframe} />
        </Panel>
      ),
    });
  }
  if (layout.panels.book) {
    const [bookBase, bookQuote] = pair.split('/');
    const depth = Math.max(bids.length, asks.length);
    topPanels.push({
      id: 'book',
      label: 'BOOK',
      flex: layout.topFlex.book,
      minWidth: 220,
      node: (
        <Panel
          id="book"
          title="ORDER BOOK"
          right={<span>DEPTH {Math.min(depth, 14)}</span>}
          noPad
          onClose={() => togglePanel('book')}
        >
          <OrderBook
            bids={bids}
            asks={asks}
            baseDecimals={baseDecimals}
            baseSymbol={bookBase}
            quoteSymbol={bookQuote}
            onPriceClick={(price) => setPrefillPrice(price)}
          />
        </Panel>
      ),
    });
  }
  if (layout.panels.tape) {
    topPanels.push({
      id: 'tape',
      label: 'TAPE',
      flex: layout.topFlex.tape,
      minWidth: 200,
      node: (
        <Panel
          id="tape"
          title="TAPE · RECENT TRADES"
          noPad
          onClose={() => togglePanel('tape')}
        >
          <RecentTrades pair={pair} />
        </Panel>
      ),
    });
  }
  if (layout.panels.entry) {
    topPanels.push({
      id: 'entry',
      label: 'ENTRY',
      flex: layout.topFlex.entry,
      minWidth: 260,
      node: (
        <Panel
          id="entry"
          title="ORDER ENTRY"
          right={<span>{pair}</span>}
          onClose={() => togglePanel('entry')}
        >
          <OrderForm pair={pair} prefillPrice={prefillPrice} />
        </Panel>
      ),
    });
  }

  const ACTIVITY_TABS = ['OPEN ORDERS', 'FILLS', 'BALANCES'] as const;

  return (
    <div className="t-layout">
      <Header
        hiddenPanels={hiddenPanels}
        onRestore={(id) => togglePanel(id as keyof PanelVis)}
        bottomHidden={!layout.bottomVisible}
        onRestoreBottom={() => setLayout(l => ({ ...l, bottomVisible: true }))}
        onOpenWallet={() => setWalletOpen(true)}
      />

      <TeeProxyStatus />

      <MarketStats
        pair={pair}
        pairs={PAIRS.map(p => p.name)}
        onPairChange={setPair}
        bids={bids}
        asks={asks}
        matches={matches}
      />

      <ResizableRow panels={topPanels} onResizePair={setTopFlex} />

      {layout.bottomVisible && (
        <>
          <div
            className="v-resizer"
            onMouseDown={startBottomResize}
          />
          <div style={{
            height: layout.bottomHeight,
            display: 'flex',
            flexDirection: 'column',
            background: 'var(--bg-1)',
            borderTop: '1px solid var(--line)',
            overflow: 'hidden',
            flexShrink: 0,
          }}>
            <div className="panel-head">
              <div className="tabs-bar" style={{ border: 'none', height: '100%', flex: 1 }}>
                {ACTIVITY_TABS.map(t => (
                  <button
                    key={t}
                    className={activityTab === t ? 'active' : ''}
                    onClick={() => setActivityTab(t)}
                  >
                    {t}
                  </button>
                ))}
              </div>
              <button className="panel-close" onClick={() => setLayout(l => ({ ...l, bottomVisible: false }))}>×</button>
            </div>
            <div style={{ flex: 1, overflow: 'auto' }}>
              {activityTab === 'OPEN ORDERS' && <OpenOrders />}
              {activityTab === 'FILLS' && <MyFills pair={pair} />}
              {activityTab === 'BALANCES' && <Balances />}
            </div>
          </div>
        </>
      )}

      <div className="statusbar">
        <span className="statusbar-cell"><span className="dim">NETWORK</span> COSTON2</span>
        <span className="statusbar-cell"><span className="dim">PAIR</span> {pair}</span>
        <span className="statusbar-cell push">
          <span className="dot" />
          <span className="dim">TEE</span> ONLINE
        </span>
      </div>

      <WalletModal open={walletOpen} onClose={() => setWalletOpen(false)} />
    </div>
  );
}
