package balance

import (
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

var (
	tokenA = common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	tokenB = common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	user1  = "0x1111111111111111111111111111111111111111"
	user2  = "0x2222222222222222222222222222222222222222"
)

func TestDeposit(t *testing.T) {
	m := NewManager()
	if err := m.Deposit(user1, tokenA, 1000); err != nil {
		t.Fatal(err)
	}

	bal := m.Get(user1, tokenA)
	if bal.Available != 1000 {
		t.Fatalf("expected available 1000, got %d", bal.Available)
	}
	if bal.Held != 0 {
		t.Fatalf("expected held 0, got %d", bal.Held)
	}
}

func TestDeposit_ZeroAmount(t *testing.T) {
	m := NewManager()
	if err := m.Deposit(user1, tokenA, 0); err != ErrZeroAmount {
		t.Fatalf("expected ErrZeroAmount, got %v", err)
	}
}

func TestWithdraw_Success(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 1000)

	if err := m.Withdraw(user1, tokenA, 400); err != nil {
		t.Fatal(err)
	}

	bal := m.Get(user1, tokenA)
	if bal.Available != 600 {
		t.Fatalf("expected available 600, got %d", bal.Available)
	}
}

func TestWithdraw_Insufficient(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 100)

	if err := m.Withdraw(user1, tokenA, 200); err != ErrInsufficientBalance {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestHold_Success(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 1000)

	if err := m.Hold(user1, tokenA, 300); err != nil {
		t.Fatal(err)
	}

	bal := m.Get(user1, tokenA)
	if bal.Available != 700 {
		t.Fatalf("expected available 700, got %d", bal.Available)
	}
	if bal.Held != 300 {
		t.Fatalf("expected held 300, got %d", bal.Held)
	}
}

func TestHold_Insufficient(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 100)

	if err := m.Hold(user1, tokenA, 200); err != ErrInsufficientBalance {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestRelease(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 1000)
	_ = m.Hold(user1, tokenA, 300)

	if err := m.Release(user1, tokenA, 200); err != nil {
		t.Fatal(err)
	}

	bal := m.Get(user1, tokenA)
	if bal.Available != 900 {
		t.Fatalf("expected available 900, got %d", bal.Available)
	}
	if bal.Held != 100 {
		t.Fatalf("expected held 100, got %d", bal.Held)
	}
}

func TestRelease_InsufficientHeld(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 1000)
	_ = m.Hold(user1, tokenA, 100)

	if err := m.Release(user1, tokenA, 200); err != ErrInsufficientHeld {
		t.Fatalf("expected ErrInsufficientHeld, got %v", err)
	}
}

func TestTransfer(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 1000)
	_ = m.Hold(user1, tokenA, 500)

	if err := m.Transfer(user1, user2, tokenA, 300); err != nil {
		t.Fatal(err)
	}

	bal1 := m.Get(user1, tokenA)
	if bal1.Held != 200 {
		t.Fatalf("expected user1 held 200, got %d", bal1.Held)
	}

	bal2 := m.Get(user2, tokenA)
	if bal2.Available != 300 {
		t.Fatalf("expected user2 available 300, got %d", bal2.Available)
	}
}

func TestTransfer_InsufficientHeld(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 100)
	_ = m.Hold(user1, tokenA, 50)

	if err := m.Transfer(user1, user2, tokenA, 100); err != ErrInsufficientHeld {
		t.Fatalf("expected ErrInsufficientHeld, got %v", err)
	}
}

func TestMultipleTokens(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 1000)
	_ = m.Deposit(user1, tokenB, 500)

	balA := m.Get(user1, tokenA)
	balB := m.Get(user1, tokenB)

	if balA.Available != 1000 {
		t.Fatalf("expected tokenA available 1000, got %d", balA.Available)
	}
	if balB.Available != 500 {
		t.Fatalf("expected tokenB available 500, got %d", balB.Available)
	}
}

func TestGetAll(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 1000)
	_ = m.Deposit(user1, tokenB, 500)

	all := m.GetAll(user1)
	if len(all) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(all))
	}
	if all[tokenA].Available != 1000 {
		t.Fatalf("expected tokenA available 1000, got %d", all[tokenA].Available)
	}
}

func TestGetAll_NoUser(t *testing.T) {
	m := NewManager()
	all := m.GetAll("nonexistent")
	if all != nil {
		t.Fatalf("expected nil for nonexistent user, got %v", all)
	}
}

func TestConcurrency(t *testing.T) {
	m := NewManager()
	_ = m.Deposit(user1, tokenA, 100000)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = m.Hold(user1, tokenA, 1)
		}()
		go func() {
			defer wg.Done()
			_ = m.Get(user1, tokenA)
		}()
	}
	wg.Wait()

	bal := m.Get(user1, tokenA)
	// At most 100 holds of 1 each.
	if bal.Available+bal.Held != 100000 {
		t.Fatalf("expected total 100000, got available=%d held=%d", bal.Available, bal.Held)
	}
}
