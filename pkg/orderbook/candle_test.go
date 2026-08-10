package orderbook

import "testing"

func TestParseTimeframe(t *testing.T) {
	cases := []struct {
		in   string
		want Timeframe
	}{
		{"", TF1m},
		{"1m", TF1m},
		{"5m", TF5m},
		{"15m", TF15m},
		{"1h", TF1h},
		{"4h", TF4h},
		{"1D", TF1d},
		{"1d", TF1d},
	}
	for _, c := range cases {
		got, err := ParseTimeframe(c.in)
		if err != nil {
			t.Errorf("ParseTimeframe(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseTimeframe(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	if _, err := ParseTimeframe("2h"); err == nil {
		t.Error("ParseTimeframe(\"2h\") expected error, got nil")
	}
}

func TestTimeframeString(t *testing.T) {
	cases := map[Timeframe]string{
		TF1m: "1m", TF5m: "5m", TF15m: "15m",
		TF1h: "1h", TF4h: "4h", TF1d: "1D",
	}
	for tf, want := range cases {
		if got := tf.String(); got != want {
			t.Errorf("%v.String() = %q, want %q", int64(tf), got, want)
		}
	}
}

func TestMatchSeconds(t *testing.T) {
	// 1_700_000_000 unix seconds expressed in nanoseconds.
	m := Match{Timestamp: 1_700_000_000_123_456_789}
	if got := m.Seconds(); got != 1_700_000_000 {
		t.Fatalf("Seconds() = %d, want 1700000000", got)
	}
}

func TestBucketAlignmentAllTimeframes(t *testing.T) {
	// 1_699_920_000 = 19675 * 86400, so it's divisible by every timeframe we use.
	const base int64 = 1_699_920_000
	const offset int64 = 23
	secs := base + offset

	for _, tf := range Timeframes {
		got := secs - secs%int64(tf)
		if got != base {
			t.Errorf("%v bucket: got %d, want %d", tf, got, base)
		}
		if got%int64(tf) != 0 {
			t.Errorf("%v bucket %d not aligned to %d", tf, got, int64(tf))
		}
	}
}

// TestCandleRollingViaRing exercises the bucketing pattern used by
// extension.updateCandles: same-bucket matches mutate the latest candle,
// new-bucket matches push a fresh candle.
func TestCandleRollingViaRing(t *testing.T) {
	r := NewRing[Candle](100)

	push := func(ts int64, price, qty uint64, tf Timeframe) {
		secs := ts / 1_000_000_000
		bucket := secs - secs%int64(tf)
		last, ok := r.Latest()
		if !ok || last.OpenTime != bucket {
			open := price
			if ok {
				open = last.Close
			}
			high, low := open, open
			if price > high {
				high = price
			}
			if price < low {
				low = price
			}
			r.Push(Candle{
				OpenTime: bucket, Open: open, High: high, Low: low, Close: price,
				Volume: qty, Trades: 1,
			})
			return
		}
		if price > last.High {
			last.High = price
		}
		if price < last.Low {
			last.Low = price
		}
		last.Close = price
		last.Volume += qty
		last.Trades++
		r.SetLatest(last)
	}

	// Three trades within one 1m bucket (at +0s, +20s, +50s). Base is 1m-aligned.
	const base = int64(1_699_920_000) // 19675 * 86400 — aligned to every TF
	push(base*1_000_000_000, 100, 5, TF1m)
	push((base+20)*1_000_000_000, 110, 3, TF1m)
	push((base+50)*1_000_000_000, 95, 2, TF1m)

	if r.Len() != 1 {
		t.Fatalf("len after 3 same-bucket pushes: got %d, want 1", r.Len())
	}
	c, _ := r.Latest()
	if c.OpenTime != base || c.Open != 100 || c.High != 110 || c.Low != 95 || c.Close != 95 || c.Volume != 10 || c.Trades != 3 {
		t.Fatalf("OHLCV: got %+v", c)
	}

	// Trade in the next 1m bucket (+90s) starts a new candle.
	push((base+90)*1_000_000_000, 120, 4, TF1m)
	if r.Len() != 2 {
		t.Fatalf("len after next-bucket push: got %d, want 2", r.Len())
	}
	c, _ = r.Latest()
	if c.OpenTime != base+60 || c.Open != 95 || c.High != 120 || c.Low != 95 || c.Close != 120 || c.Volume != 4 || c.Trades != 1 {
		t.Fatalf("new bucket: got %+v", c)
	}
}

// TestCandleContinuityAcrossBuckets verifies that consecutive candles share an
// edge: the next bucket's Open equals the previous bucket's Close.
func TestCandleContinuityAcrossBuckets(t *testing.T) {
	r := NewRing[Candle](100)

	push := func(ts int64, price, qty uint64, tf Timeframe) {
		secs := ts / 1_000_000_000
		bucket := secs - secs%int64(tf)
		last, ok := r.Latest()
		if !ok || last.OpenTime != bucket {
			open := price
			if ok {
				open = last.Close
			}
			high, low := open, open
			if price > high {
				high = price
			}
			if price < low {
				low = price
			}
			r.Push(Candle{
				OpenTime: bucket, Open: open, High: high, Low: low, Close: price,
				Volume: qty, Trades: 1,
			})
			return
		}
		if price > last.High {
			last.High = price
		}
		if price < last.Low {
			last.Low = price
		}
		last.Close = price
		last.Volume += qty
		last.Trades++
		r.SetLatest(last)
	}

	const base = int64(1_699_920_000)
	push(base*1_000_000_000, 100, 1, TF1m)      // bucket 0: O=H=L=C=100
	push((base+30)*1_000_000_000, 80, 1, TF1m)  // bucket 0: C=80, L=80
	push((base+90)*1_000_000_000, 130, 1, TF1m) // bucket 1: O=80 (prev close), C=130
	push((base+200)*1_000_000_000, 60, 1, TF1m) // bucket 3 (skipped 2): O=130, C=60
	push((base+260)*1_000_000_000, 60, 1, TF1m) // bucket 4: O=60, C=60

	all := r.Snapshot()
	for i := 1; i < len(all); i++ {
		if all[i].Open != all[i-1].Close {
			t.Errorf("candle[%d].Open=%d != candle[%d].Close=%d", i, all[i].Open, i-1, all[i-1].Close)
		}
	}
}
