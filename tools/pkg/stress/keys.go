// Package stress provides a parallel mock-trader framework for stress-testing
// the orderbook extension.
package stress

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/crypto"
)

// TraderKeysFile is the persisted shape of the cache file.
type TraderKeysFile struct {
	Version int      `json:"version"`
	Keys    []string `json:"keys"` // hex-encoded private keys, no 0x prefix
}

// GenerateOrLoadTraderKeys returns n deterministic trader keys, reading from
// cacheFile if present and extending the list when n grows. Keys are appended
// only; existing entries are never shuffled, so trader index is stable across runs.
func GenerateOrLoadTraderKeys(n int, cacheFile string) ([]*ecdsa.PrivateKey, error) {
	if n <= 0 {
		return nil, fmt.Errorf("n must be positive, got %d", n)
	}

	var file TraderKeysFile
	raw, err := os.ReadFile(cacheFile)
	if err == nil {
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", cacheFile, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", cacheFile, err)
	}

	keys := make([]*ecdsa.PrivateKey, 0, n)
	for _, h := range file.Keys {
		k, err := crypto.HexToECDSA(h)
		if err != nil {
			return nil, fmt.Errorf("decoding cached key: %w", err)
		}
		keys = append(keys, k)
	}

	for len(keys) < n {
		k, err := crypto.GenerateKey()
		if err != nil {
			return nil, fmt.Errorf("generating key: %w", err)
		}
		keys = append(keys, k)
	}

	// Persist (trim to n so oversized caches shrink cleanly).
	out := TraderKeysFile{Version: 1, Keys: make([]string, n)}
	for i := 0; i < n; i++ {
		out.Keys[i] = hex.EncodeToString(crypto.FromECDSA(keys[i]))
	}

	if err := os.MkdirAll(filepath.Dir(cacheFile), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", cacheFile, err)
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	if err := os.WriteFile(cacheFile, data, 0o600); err != nil {
		return nil, fmt.Errorf("writing %s: %w", cacheFile, err)
	}

	return keys[:n], nil
}
