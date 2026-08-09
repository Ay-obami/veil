package types

import (
	"testing"

	"veil/pkg/decoder"
)

// TestRegisterDecoders_AllOPCommandsCovered asserts every OPCommand the extension
// answers has both message and result decoders. Catches drift when a new command
// is added to internal/config/config.go but forgotten here.
func TestRegisterDecoders_AllOPCommandsCovered(t *testing.T) {
	r := decoder.NewRegistry()
	RegisterDecoders(r)

	commands := []string{
		"DEPOSIT", "WITHDRAW",
		"PLACE_ORDER", "CANCEL_ORDER",
		"GET_MY_STATE", "GET_BOOK_STATE", "GET_CANDLES", "EXPORT_HISTORY",
	}

	for _, cmd := range commands {
		if _, err := r.Lookup("ORDERBOOK", cmd, decoder.KindMessage); err != nil {
			t.Errorf("no message decoder for %s: %v", cmd, err)
		}
		if _, err := r.Lookup("ORDERBOOK", cmd, decoder.KindResult); err != nil {
			t.Errorf("no result decoder for %s: %v", cmd, err)
		}
	}
}
