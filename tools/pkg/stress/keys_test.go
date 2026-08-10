package stress

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestGenerateOrLoadTraderKeys_GeneratesThenReloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "traders.json")

	gen, err := GenerateOrLoadTraderKeys(5, path)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen) != 5 {
		t.Fatalf("want 5 keys, got %d", len(gen))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}

	reloaded, err := GenerateOrLoadTraderKeys(5, path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded) != 5 {
		t.Fatalf("want 5 keys on reload, got %d", len(reloaded))
	}
	for i := range gen {
		genAddr := crypto.PubkeyToAddress(gen[i].PublicKey).Hex()
		reAddr := crypto.PubkeyToAddress(reloaded[i].PublicKey).Hex()
		if genAddr != reAddr {
			t.Fatalf("key %d: generated %s, reloaded %s", i, genAddr, reAddr)
		}
	}
}

func TestGenerateOrLoadTraderKeys_ExtendsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "traders.json")

	first, err := GenerateOrLoadTraderKeys(3, path)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := GenerateOrLoadTraderKeys(5, path)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(second) != 5 {
		t.Fatalf("want 5 after extend, got %d", len(second))
	}
	for i := 0; i < 3; i++ {
		a := crypto.PubkeyToAddress(first[i].PublicKey).Hex()
		b := crypto.PubkeyToAddress(second[i].PublicKey).Hex()
		if a != b {
			t.Fatalf("key %d changed on extend: %s vs %s", i, a, b)
		}
	}
}
