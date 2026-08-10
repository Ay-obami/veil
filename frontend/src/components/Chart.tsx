import { useEffect, useMemo, useRef } from 'react';
import {
  createChart,
  CandlestickSeries,
  CrosshairMode,
  type IChartApi,
  type ISeriesApi,
  type UTCTimestamp,
} from 'lightweight-charts';
import { useCandles } from '../hooks/useCandles';
import { fromServerCandles, type Candle, type Timeframe, TF_SECONDS } from '../lib/candles';
import { formatHumanAdaptive } from '../lib/price';

interface ChartProps {
  pair: string;
  timeframe: Timeframe;
}

function cssVar(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback;
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return v || fallback;
}

export function Chart({ pair, timeframe }: ChartProps) {
  const { data } = useCandles(pair, timeframe);
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null);
  const lastTfRef = useRef<Timeframe | null>(null);
  const lastPairRef = useRef<string | null>(null);
  const prevCandlesRef = useRef<Candle[]>([]);

  const candles = useMemo(
    () => fromServerCandles(data?.candles ?? [], timeframe),
    [data, timeframe],
  );

  useEffect(() => {
    if (!containerRef.current) return;

    const fgMute = cssVar('--fg-mute', '#5a5a54');
    const line = cssVar('--line', '#1f1f1f');
    const bid = cssVar('--bid', '#26a269');
    const ask = cssVar('--ask', '#c01c28');
    const accent = cssVar('--accent', '#c96aa8');

    const chart = createChart(containerRef.current, {
      width: containerRef.current.clientWidth,
      height: containerRef.current.clientHeight,
      layout: {
        background: { color: 'transparent' },
        textColor: fgMute,
        fontFamily: 'var(--f-mono, ui-monospace, monospace)',
        fontSize: 10,
      },
      grid: {
        vertLines: { color: line },
        horzLines: { color: line },
      },
      rightPriceScale: {
        borderColor: line,
        scaleMargins: { top: 0.1, bottom: 0.1 },
      },
      timeScale: {
        borderColor: line,
        timeVisible: true,
        secondsVisible: false,
      },
      crosshair: {
        mode: CrosshairMode.Normal,
        vertLine: { color: fgMute, width: 1, style: 2, labelBackgroundColor: accent },
        horzLine: { color: fgMute, width: 1, style: 2, labelBackgroundColor: accent },
      },
      localization: { priceFormatter: formatHumanAdaptive },
    });

    const series = chart.addSeries(CandlestickSeries, {
      upColor: bid,
      downColor: ask,
      wickUpColor: bid,
      wickDownColor: ask,
      borderVisible: false,
      priceFormat: { type: 'price', precision: 6, minMove: 0.000001 },
    });

    chartRef.current = chart;
    seriesRef.current = series;

    const ro = new ResizeObserver(() => {
      if (!containerRef.current || !chartRef.current) return;
      chartRef.current.applyOptions({
        width: containerRef.current.clientWidth,
        height: containerRef.current.clientHeight,
      });
    });
    ro.observe(containerRef.current);

    return () => {
      ro.disconnect();
      chart.remove();
      chartRef.current = null;
      seriesRef.current = null;
      lastTfRef.current = null;
      lastPairRef.current = null;
    };
  }, []);

  useEffect(() => {
    const series = seriesRef.current;
    const chart = chartRef.current;
    if (!series || !chart) return;

    const toBar = (c: Candle) => ({
      time: c.time as UTCTimestamp,
      open: c.open,
      high: c.high,
      low: c.low,
      close: c.close,
    });
    const sameBar = (a: Candle, b: Candle) =>
      a.time === b.time && a.open === b.open && a.high === b.high && a.low === b.low && a.close === b.close;

    const prev = prevCandlesRef.current;
    const n = candles.length;
    const tfChanged = lastTfRef.current !== timeframe;
    const pairChanged = lastPairRef.current !== pair;

    if (!tfChanged && !pairChanged && n > 0 && n === prev.length) {
      // Check if only the last bar differs — common live-tick case, use update() to avoid full redraw.
      let diffIdx = -1;
      for (let i = 0; i < n; i++) {
        if (!sameBar(candles[i], prev[i])) {
          if (diffIdx !== -1) { diffIdx = -2; break; }
          diffIdx = i;
        }
      }
      if (diffIdx === -1) {
        prevCandlesRef.current = candles;
        return; // identical — nothing to redraw
      }
      if (diffIdx === n - 1) {
        series.update(toBar(candles[n - 1]));
        prevCandlesRef.current = candles;
        return;
      }
    }

    series.setData(candles.map(toBar));
    prevCandlesRef.current = candles;

    if (tfChanged || pairChanged) {
      const last = candles[n - 1]?.time;
      if (typeof last === 'number') {
        const span = Math.max(60, TF_SECONDS[timeframe] * 60);
        chart.timeScale().setVisibleRange({
          from: (last - span) as UTCTimestamp,
          to: (last + TF_SECONDS[timeframe]) as UTCTimestamp,
        });
      } else {
        chart.timeScale().fitContent();
      }
      // Force price scale to refit to the new series' range — otherwise a
      // switch from e.g. BTC (80k) to ETH (3k) keeps the old scale.
      chart.priceScale('right').applyOptions({ autoScale: true });
      lastTfRef.current = timeframe;
      lastPairRef.current = pair;
    }
  }, [candles, timeframe, pair]);

  const hasData = candles.length > 0;

  return (
    <div style={{ position: 'relative', width: '100%', height: '100%' }}>
      <div ref={containerRef} style={{ width: '100%', height: '100%' }} />
      {!hasData && (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            pointerEvents: 'none',
          }}
        >
          <div className="empty-hint">NO TRADES · WAITING FOR MARKET DATA</div>
        </div>
      )}
    </div>
  );
}
