package balance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
)

// SetPersistPath enables writing the manager's full balance map to disk
// after every state mutation, and loads any pre-existing snapshot from
// the same path. Pass "" to disable persistence (the default).
//
// On load, any non-zero Held amount is migrated back to Available.
// Rationale: this manager has no knowledge of the orders that originally
// held those funds; after a restart, those orders are gone, so the held
// portion of a user's wealth would otherwise be permanently stuck.
//
// Only one path is supported per Manager instance.
func (m *Manager) SetPersistPath(path string) error {
	m.mu.Lock()
	m.persistPath = path
	m.mu.Unlock()
	if path == "" {
		return nil
	}
	if err := m.load(); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("loading balance snapshot: %w", err)
	}
	return nil
}

// PersistPath returns the configured persistence path, or "" if disabled.
func (m *Manager) PersistPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.persistPath
}

// snapshot is the on-disk shape; it serializes a flat map with hex token
// addresses so JSON keys are stable across restarts.
type snapshot struct {
	Balances map[string]map[string]TokenBalance `json:"balances"`
}

// save marshals the current balances and atomically replaces the file.
// Caller must hold m.mu (read or write).
func (m *Manager) save() error {
	if m.persistPath == "" {
		return nil
	}
	snap := snapshot{Balances: make(map[string]map[string]TokenBalance, len(m.balances))}
	for user, tokens := range m.balances {
		out := make(map[string]TokenBalance, len(tokens))
		for addr, tb := range tokens {
			out[addr.Hex()] = *tb
		}
		snap.Balances[user] = out
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.persistPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".balances-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, m.persistPath)
}

// load reads the snapshot file (if any) and populates m.balances.
// Caller must NOT hold m.mu — this method takes it.
func (m *Manager) load() error {
	data, err := os.ReadFile(m.persistPath)
	if err != nil {
		return err
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("decoding balance snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for user, tokens := range snap.Balances {
		userMap := make(map[common.Address]*TokenBalance, len(tokens))
		for hex, tb := range tokens {
			addr := common.HexToAddress(hex)
			// Migrate Held -> Available: the orders that held these funds are gone.
			tb.Available += tb.Held
			tb.Held = 0
			userMap[addr] = &TokenBalance{Available: tb.Available, Held: tb.Held}
		}
		m.balances[user] = userMap
	}
	return nil
}
