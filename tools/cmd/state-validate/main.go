// state-validate drives the encrypted-state validation against a running
// extension WITHOUT a proxy, by POSTing Action JSON straight to the extension's
// POST /action endpoint (exactly what tee-node's forward router does internally).
// Used by the staging runbook (docs/state-backup-staging.md) to:
//
//	-op query   : GET_MY_STATE for a user (verify balances survived restart/restore)
//	-op restore : drive the full RESTORE_BEGIN / RESTORE_SUBMIT handshake (V4),
//	              reusing the security-critical logic in tools/pkg/restore.
//
// This is a staging/dev tool: it accepts any attestation (a plain Confidential VM
// has no Confidential Space teeserver) and trusts the blob's own signer as the
// expected TEE address (TOFU) — the TEE-side restore does not depend on that
// client-side check. Do NOT use as a production restore client; use admin-restore.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"veil/pkg/state"
	obtypes "veil/pkg/types"
	"veil/tools/pkg/restore"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

func main() {
	extURL := flag.String("ext", "http://localhost:7702", "extension base URL (POST /action)")
	op := flag.String("op", "query", "operation: query | restore")
	user := flag.String("user", "", "user address for -op query")
	keyHex := flag.String("key", os.Getenv("ADMIN_PRIVATE_KEY"), "admin secp256k1 private key (or ADMIN_PRIVATE_KEY); -op restore")
	blobPath := flag.String("blob", "", "local path to latest.bin; -op restore")
	flag.Parse()

	switch *op {
	case "query":
		if *user == "" {
			fatal("-user is required for -op query")
		}
		runQuery(*extURL, *user)
	case "restore":
		runRestore(*extURL, *keyHex, *blobPath)
	default:
		fatal(fmt.Sprintf("unknown -op %q", *op))
	}
}

func runQuery(extURL, user string) {
	ar, err := postAction(extURL, "GET_MY_STATE", obtypes.GetMyStateRequest{Sender: strings.ToLower(user)})
	if err != nil {
		fatal(err.Error())
	}
	var st obtypes.GetMyStateResponse
	if err := json.Unmarshal(ar.Data, &st); err != nil {
		fatal(fmt.Sprintf("decode state: %v", err))
	}
	fmt.Printf("state for %s:\n", user)
	if len(st.Balances) == 0 {
		fmt.Println("  (no balances)")
	}
	for token, bal := range st.Balances {
		fmt.Printf("  %s  available=%d held=%d\n", token.Hex(), bal.Available, bal.Held)
	}
	fmt.Printf("  open orders: %d, matches: %d\n", len(st.OpenOrders), len(st.Matches))
}

func runRestore(extURL, keyHex, blobPath string) {
	if keyHex == "" {
		fatal("admin key required (-key or ADMIN_PRIVATE_KEY)")
	}
	if blobPath == "" {
		fatal("-blob (local path to latest.bin) is required")
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(keyHex, "0x"))
	if err != nil {
		fatal(fmt.Sprintf("bad admin key: %v", err))
	}
	adminAddr := crypto.PubkeyToAddress(key.PublicKey)

	// TOFU: recover the blob's own signer and use it as the expected TEE address.
	raw, err := os.ReadFile(blobPath)
	if err != nil {
		fatal(fmt.Sprintf("read blob: %v", err))
	}
	pb, err := state.ParseBlob(raw)
	if err != nil {
		fatal(fmt.Sprintf("parse blob: %v", err))
	}
	signer, err := state.RecoverBlobSigner(pb)
	if err != nil {
		fatal(fmt.Sprintf("recover blob signer: %v", err))
	}
	fmt.Printf("blob signer (expected TEE addr, TOFU): %s\n", signer.Hex())

	t := &directTransport{extURL: extURL, blobPath: blobPath}
	p := restore.Params{
		Sender:          strings.ToLower(adminAddr.Hex()),
		AdminKey:        key,
		AdminAddr:       adminAddr,
		ExpectedTEEAddr: signer,
	}
	if err := restore.Run(context.Background(), t, acceptAllVerifier{}, p); err != nil {
		fatal(err.Error())
	}
	fmt.Println("restore submitted OK; extension is opening for traffic")
}

// directTransport posts Actions straight to the extension /action endpoint.
type directTransport struct {
	extURL   string
	blobPath string
}

func (d *directTransport) Begin(_ context.Context, sender string) (*restore.RestoreBeginResult, error) {
	ar, err := postAction(d.extURL, "RESTORE_BEGIN", obtypes.RestoreBeginRequest{Sender: sender})
	if err != nil {
		return nil, err
	}
	var resp obtypes.RestoreBeginResponse
	if err := json.Unmarshal(ar.Data, &resp); err != nil {
		return nil, fmt.Errorf("decode begin response: %w", err)
	}
	out := &restore.RestoreBeginResult{CreatedAt: resp.CreatedAt, Attestation: resp.Attestation}
	copy(out.EphPub[:], resp.EphPub)
	copy(out.BlobID[:], resp.BlobID)
	raw, err := os.ReadFile(d.blobPath)
	if err != nil {
		return nil, fmt.Errorf("read blob: %w", err)
	}
	out.RawBlob = raw
	return out, nil
}

func (d *directTransport) Submit(_ context.Context, sender string, adminAddr common.Address, sealed []byte) error {
	_, err := postAction(d.extURL, "RESTORE_SUBMIT", obtypes.RestoreSubmitRequest{
		Sender:    sender,
		AdminAddr: adminAddr,
		Sealed:    sealed,
	})
	return err
}

// acceptAllVerifier skips attestation checks (plain CVM has no CS teeserver).
type acceptAllVerifier struct{}

func (acceptAllVerifier) Verify(_ []byte, _, _ [32]byte, _ string) error { return nil }

// postAction wraps a direct instruction in an Action and POSTs it to /action,
// returning the synchronous ActionResult.
func postAction(extURL, opCommand string, payload any) (teetypes.ActionResult, error) {
	var ar teetypes.ActionResult
	msg, err := json.Marshal(payload)
	if err != nil {
		return ar, fmt.Errorf("marshal payload: %w", err)
	}
	di := teetypes.DirectInstruction{
		OPType:    toBytes32("ORDERBOOK"),
		OPCommand: toBytes32(opCommand),
		Message:   msg,
	}
	diJSON, err := json.Marshal(di)
	if err != nil {
		return ar, fmt.Errorf("marshal instruction: %w", err)
	}
	var id common.Hash
	if _, err := rand.Read(id[:]); err != nil {
		return ar, fmt.Errorf("gen id: %w", err)
	}
	action := teetypes.Action{Data: teetypes.ActionData{ID: id, Type: teetypes.Direct, Message: diJSON}}
	body, err := json.Marshal(action)
	if err != nil {
		return ar, fmt.Errorf("marshal action: %w", err)
	}
	resp, err := http.Post(extURL+"/action", "application/json", bytes.NewReader(body))
	if err != nil {
		return ar, fmt.Errorf("POST /action: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return ar, fmt.Errorf("%s: HTTP %d: %s", opCommand, resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	if err := json.Unmarshal(rb, &ar); err != nil {
		return ar, fmt.Errorf("decode action result: %w (body=%s)", err, string(rb))
	}
	if ar.Status != 1 {
		return ar, fmt.Errorf("%s failed (status %d): %s", opCommand, ar.Status, ar.Log)
	}
	return ar, nil
}

// toBytes32 left-aligns a string into a bytes32 (like Solidity bytes32("...")).
func toBytes32(s string) common.Hash {
	var h common.Hash
	copy(h[:], s)
	return h
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "state-validate:", msg)
	os.Exit(1)
}
