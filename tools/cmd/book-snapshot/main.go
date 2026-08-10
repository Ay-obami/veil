// book-snapshot prints a one-shot summary of resting bids/asks for each pair.
package main

import (
	"flag"
	"fmt"
	"os"

	"veil/tools/pkg/configs"
	instrutils "veil/tools/pkg/utils"
)

type getBookStateReq struct {
	Sender string `json:"sender,omitempty"`
}

type level struct {
	Price    uint64 `json:"price"`
	Quantity uint64 `json:"quantity"`
}

type pairBook struct {
	Bids []level `json:"bids"`
	Asks []level `json:"asks"`
}

type stateResp struct {
	State struct {
		Pairs map[string]pairBook `json:"pairs"`
	} `json:"state"`
}

func main() {
	pf := flag.String("p", configs.ExtensionProxyURL, "extension proxy url")
	flag.Parse()

	var s stateResp
	if err := instrutils.SendDirectAndPoll(*pf, "GET_BOOK_STATE", getBookStateReq{}, &s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for pair, p := range s.State.Pairs {
		fmt.Printf("=== %s ===\n", pair)
		fmt.Printf("Bids: %d  Asks: %d\n", len(p.Bids), len(p.Asks))
		fmt.Println("  Bids (top 8 by price desc):")
		for i, b := range p.Bids {
			if i >= 8 {
				break
			}
			fmt.Printf("    %12d @ %12d\n", b.Quantity, b.Price)
		}
		fmt.Println("  Asks (top 8 by price asc):")
		for i, a := range p.Asks {
			if i >= 8 {
				break
			}
			fmt.Printf("    %12d @ %12d\n", a.Quantity, a.Price)
		}
		fmt.Println()
	}
}
