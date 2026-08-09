import { useState, useEffect } from 'react';
import { useAccount } from 'wagmi';
import { formatUnits } from 'viem';
import { usePlaceOrder, PLACE_ORDER_STEPS, PLACE_ORDER_STEPS_PRIVATE } from '../hooks/usePlaceOrder';
import { useMyState } from '../hooks/useMyState';
import { useWalletBalances } from '../hooks/useWalletBalances';
import { useTray } from './ui/ActionTray';
import { PAIRS } from '../config/generated';
import type { PlaceOrderReq } from '../lib/orderbook';

interface OrderFormProps {
  pair: string;
  prefillPrice: number | null;
}

export function OrderForm({ pair, prefillPrice }: OrderFormProps) {
  const { isConnected } = useAccount();
  const [side, setSide] = useState<'buy' | 'sell'>('buy');
  const [orderType, setOrderType] = useState<'limit' | 'market'>('limit');
  const [price, setPrice] = useState('');
  const [quantity, setQuantity] = useState('');
  const [privateOrder, setPrivateOrder] = useState(false);
  const tray = useTray();
  const placeOrder = usePlaceOrder();
  const { balances } = useMyState();
  const { tokenInfo } = useWalletBalances();

  // Pair info
  const [base, quote] = pair.split('/');
  const pairConfig = PAIRS.find(p => p.name === pair);
  const baseToken = pairConfig?.baseToken.toLowerCase() ?? '';
  const quoteToken = pairConfig?.quoteToken.toLowerCase() ?? '';
  const baseDecimals = tokenInfo[baseToken]?.decimals;
  const quoteDecimals = tokenInfo[quoteToken]?.decimals;

  // TEE available balances (raw integers) → human-readable
  const baseRaw = balances[baseToken]?.available ?? 0;
  const quoteRaw = balances[quoteToken]?.available ?? 0;
  const baseAvail = baseDecimals !== undefined
    ? parseFloat(formatUnits(BigInt(Math.floor(baseRaw)), baseDecimals))
    : baseRaw;
  const quoteAvail = quoteDecimals !== undefined
    ? parseFloat(formatUnits(BigInt(Math.floor(quoteRaw)), quoteDecimals))
    : quoteRaw;

  // Prefill price from order book click — prefillPrice is already human-readable
  useEffect(() => {
    if (prefillPrice !== null) {
      setPrice(String(prefillPrice));
    }
  }, [prefillPrice]);

  // Private orders require a fixed amount to prove solvency for — not
  // meaningful for market orders (see usePlaceOrder.ts and
  // internal/extension/solvency.go). Reset rather than let the UI reach
  // an invalid combination the backend would reject anyway.
  useEffect(() => {
    if (orderType === 'market' && privateOrder) {
      setPrivateOrder(false);
    }
  }, [orderType, privateOrder]);

  // Percentage fill buttons
  function fillPct(pct: number) {
    const p = parseFloat(price);
    if (side === 'sell') {
      setQuantity((baseAvail * pct / 100).toFixed(4));
    } else {
      if (p > 0) {
        setQuantity(((quoteAvail * pct / 100) / p).toFixed(4));
      }
    }
  }

  const priceNum = parseFloat(price);
  const qtyNum = parseFloat(quantity);
  const total = (priceNum || 0) * (qtyNum || 0);

  const canSubmit = isConnected && !placeOrder.isPending && qtyNum > 0 &&
    (orderType === 'market' || priceNum > 0);

  async function submit() {
    if (!canSubmit) return;
    const req: Omit<PlaceOrderReq, 'sender' | 'solvencyProof' | 'solvencyPublicSignals'> = {
      pair,
      side,
      type: orderType,
      price: orderType === 'limit' ? priceNum : 0,
      quantity: qtyNum,
    };
    const job = tray.start({
      title: `${side.toUpperCase()} ${qtyNum} ${base}${
        orderType === 'limit' ? ` @ ${priceNum}` : ' at market'
      }${privateOrder ? ' (private)' : ''}`,
      steps: privateOrder ? PLACE_ORDER_STEPS_PRIVATE : PLACE_ORDER_STEPS,
    });
    try {
      const result = await placeOrder.mutateAsync({ ...req, privateOrder, report: job });
      job.finish({
        summary: {
          status: result.status,
          fills: String(result.matches?.length ?? 0),
        },
      });
      setQuantity('');
    } catch (e) {
      job.fail({ message: e instanceof Error ? e.message : 'Order failed' });
    }
  }

  const submitLabel = !isConnected
    ? 'CONNECT WALLET'
    : placeOrder.isPending
    ? 'SENDING…'
    : `${side.toUpperCase()} ${base}`;

  return (
    <div className="oe">
      {/* BUY / SELL toggle */}
      <div className="oe-side-toggle">
        <button
          className={`buy${side === 'buy' ? ' active' : ''}`}
          onClick={() => setSide('buy')}
        >
          BUY
        </button>
        <button
          className={`sell${side === 'sell' ? ' active' : ''}`}
          onClick={() => setSide('sell')}
        >
          SELL
        </button>
      </div>

      {/* LIMIT / MARKET */}
      <div className="oe-type">
        <button
          className={orderType === 'limit' ? 'active' : ''}
          onClick={() => setOrderType('limit')}
        >
          LIMIT
        </button>
        <button
          className={orderType === 'market' ? 'active' : ''}
          onClick={() => setOrderType('market')}
        >
          MARKET
        </button>
      </div>

      {/* Price field */}
      <div className="oe-field">
        <span className="lbl">PRICE ({quote})</span>
        <input
          type="number"
          min="0"
          step="0.000001"
          value={orderType === 'market' ? '' : price}
          onChange={e => setPrice(e.target.value)}
          disabled={orderType === 'market'}
          placeholder={orderType === 'market' ? 'MARKET PRICE' : '0.000000'}
        />
      </div>

      {/* Size field */}
      <div className="oe-field">
        <span className="lbl">SIZE ({base})</span>
        <input
          type="number"
          min="0"
          step="0.0001"
          value={quantity}
          onChange={e => setQuantity(e.target.value)}
          placeholder="0.0000"
        />
      </div>

      {/* Quick fill */}
      <div className="oe-quick">
        {[25, 50, 75, 100].map(p => (
          <button key={p} onClick={() => fillPct(p)}>{p}%</button>
        ))}
      </div>

      {/* Summary */}
      <div className="oe-summary">
        <div className="row">
          <span className="label">AVAILABLE</span>
          <span className="value">
            {side === 'buy'
              ? `${quoteAvail.toFixed(4)} ${quote}`
              : `${baseAvail.toFixed(4)} ${base}`}
          </span>
        </div>
        <div className="row">
          <span className="label">TOTAL</span>
          <span className="value">{total > 0 ? total.toFixed(4) : '—'} {quote}</span>
        </div>
        <div className="row">
          <span className="label">FEE</span>
          <span className="value" style={{ color: 'var(--fg-mute)' }}>0.000</span>
        </div>
      </div>

      {/* Private order (ZK solvency proof) toggle */}
      <div className="oe-field" style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
        <input
          type="checkbox"
          id="oe-private"
          checked={privateOrder}
          disabled={orderType === 'market'}
          onChange={e => setPrivateOrder(e.target.checked)}
        />
        <label htmlFor="oe-private" className="lbl" style={{ cursor: orderType === 'market' ? 'default' : 'pointer' }}>
          PRIVATE ORDER
        </label>
      </div>
      {privateOrder && (
        <div style={{ fontSize: '11px', color: 'var(--fg-mute)', marginTop: '-8px', marginBottom: '4px' }}>
          Price and size stay off the public book — a ZK proof confirms
          you can cover this order without revealing your balance.
          {orderType === 'market' && ' (limit orders only)'}
        </div>
      )}

      {/* Submit */}
      <button
        className={`oe-submit ${side}`}
        disabled={!canSubmit}
        onClick={submit}
      >
        {submitLabel}
      </button>
    </div>
  );
}
