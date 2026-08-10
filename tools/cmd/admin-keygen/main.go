// admin-keygen derives the admin restore-recipient config for encrypted state
// backup. For each admin secp256k1 key it prints the lowercase Ethereum address
// and the uncompressed (65-byte, 0x04-prefixed) public key, then emits ready-to-
// paste ADMIN_ADDRESSES and ADMIN_PUBLIC_KEYS lines.
//
// ADMIN_PUBLIC_KEYS is required because ECIES envelope-wrapping of DEK_blob needs
// the public key, not just the address — and `cast` has no clean way to print the
// uncompressed form. Each derived address must also appear in ADMIN_ADDRESSES or
// the TEE refuses that key as a restore recipient.
//
// Usage:
//
//	# derive from one or more existing private keys (hex, optional 0x, comma or space separated)
//	go run ./cmd/admin-keygen -keys 0xabc...,0xdef...
//	ADMIN_PRIVATE_KEYS=0xabc...,0xdef... go run ./cmd/admin-keygen
//
//	# generate N fresh admin keypairs (prints the private keys — store them safely)
//	go run ./cmd/admin-keygen -gen 2
package main

import (
	"crypto/ecdsa"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

type admin struct {
	priv    string // hex private key without 0x (only set for -gen)
	addr    string // lowercase 0x address
	pubHex  string // uncompressed pubkey, no 0x prefix
	genOnly bool
}

func main() {
	keys := flag.String("keys", os.Getenv("ADMIN_PRIVATE_KEYS"), "admin secp256k1 private keys (hex, optional 0x; comma or space separated) or ADMIN_PRIVATE_KEYS env")
	gen := flag.Int("gen", 0, "generate N fresh admin keypairs instead of deriving from -keys")
	flag.Parse()

	var admins []admin
	switch {
	case *gen > 0:
		for i := 0; i < *gen; i++ {
			pk, err := crypto.GenerateKey()
			if err != nil {
				fatal(fmt.Errorf("generate key %d: %w", i, err))
			}
			a := derive(pk)
			a.priv = hex.EncodeToString(crypto.FromECDSA(pk))
			a.genOnly = true
			admins = append(admins, a)
		}
	default:
		fields := strings.FieldsFunc(*keys, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
		if len(fields) == 0 {
			fmt.Fprintln(os.Stderr, "no keys: pass -keys / ADMIN_PRIVATE_KEYS, or -gen N to generate fresh ones")
			flag.Usage()
			os.Exit(2)
		}
		for _, f := range fields {
			pk, err := crypto.HexToECDSA(strings.TrimPrefix(strings.TrimPrefix(f, "0x"), "0X"))
			if err != nil {
				fatal(fmt.Errorf("parse key %q: %w", f, err))
			}
			admins = append(admins, derive(pk))
		}
	}

	for i, a := range admins {
		fmt.Printf("# admin %d\n", i+1)
		if a.genOnly {
			fmt.Printf("#   private key : 0x%s   (store securely — never commit)\n", a.priv)
		}
		fmt.Printf("#   address     : %s\n", a.addr)
		fmt.Printf("#   public key  : 0x%s\n\n", a.pubHex)
	}

	addrs := make([]string, len(admins))
	pubs := make([]string, len(admins))
	for i, a := range admins {
		addrs[i] = a.addr
		pubs[i] = a.pubHex
	}
	fmt.Printf("ADMIN_ADDRESSES=%q\n", strings.Join(addrs, ","))
	fmt.Printf("ADMIN_PUBLIC_KEYS=%q\n", strings.Join(pubs, ","))
}

// derive returns the lowercase address and uncompressed public key for pk.
// crypto.FromECDSAPub yields the 65-byte 0x04-prefixed form that the extension's
// crypto.UnmarshalPubkey expects.
func derive(pk *ecdsa.PrivateKey) admin {
	return admin{
		addr:   strings.ToLower(crypto.PubkeyToAddress(pk.PublicKey).Hex()),
		pubHex: hex.EncodeToString(crypto.FromECDSAPub(&pk.PublicKey)),
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
