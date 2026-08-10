package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	types "github.com/flare-foundation/tee-node/pkg/types"
	"github.com/pkg/errors"
)

// ToBytes32 converts a string to a bytes32 hash (left-padded, like Solidity bytes32("...")).
func ToBytes32(s string) common.Hash {
	var h common.Hash
	copy(h[:], s)
	return h
}

// directResponse is the envelope returned by POST /direct (an Action object).
type directResponse struct {
	Data struct {
		ID common.Hash `json:"id"`
	} `json:"data"`
}

// SendDirect sends a direct instruction to the proxy and returns the action ID.
func SendDirect(proxyURL, opCommand string, payload any) (common.Hash, error) {
	msgJSON, err := json.Marshal(payload)
	if err != nil {
		return common.Hash{}, errors.Errorf("marshaling payload: %s", err)
	}

	inst := types.DirectInstruction{
		OPType:    ToBytes32("ORDERBOOK"),
		OPCommand: ToBytes32(opCommand),
		Message:   hexutil.Bytes(msgJSON),
	}

	body, err := json.Marshal(inst)
	if err != nil {
		return common.Hash{}, errors.Errorf("marshaling instruction: %s", err)
	}

	req, err := http.NewRequest("POST", proxyURL+"/direct", bytes.NewReader(body))
	if err != nil {
		return common.Hash{}, errors.Errorf("creating request: %s", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := os.Getenv("DIRECT_API_KEY"); apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return common.Hash{}, errors.Errorf("POST /direct: %s", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return common.Hash{}, fmt.Errorf("POST /direct returned %d: %s", resp.StatusCode, string(respBody))
	}

	var dr directResponse
	if err := json.Unmarshal(respBody, &dr); err != nil {
		return common.Hash{}, errors.Errorf("decoding /direct response: %s", err)
	}

	return dr.Data.ID, nil
}

// SendDirectAndPoll sends a direct instruction, polls for the result, and unmarshals
// the result data into dest. Returns an error if the instruction fails (status != 1).
func SendDirectAndPoll(proxyURL, opCommand string, payload any, dest any) error {
	actionID, err := SendDirect(proxyURL, opCommand, payload)
	if err != nil {
		return err
	}

	actionResp, err := pollDirectResult(proxyURL, actionID)
	if err != nil {
		return errors.Errorf("polling result for %s: %s", actionID.Hex(), err)
	}

	result := actionResp.Result
	if result.Status == 0 {
		return fmt.Errorf("instruction failed: %s", result.Log)
	}
	if result.Status == 2 {
		return fmt.Errorf("instruction still pending after polling (action %s)", actionID.Hex())
	}

	if dest != nil && len(result.Data) > 0 {
		if err := json.Unmarshal(result.Data, dest); err != nil {
			return errors.Errorf("unmarshaling result data: %s", err)
		}
	}

	return nil
}

// pollDirectResult polls for a direct instruction result using submissionTag=submit.
// Direct results are stored under the "submit" tag, not the default "threshold".
func pollDirectResult(nodeURL string, actionID common.Hash) (*types.ActionResponse, error) {
	url := nodeURL + "/action/result/" + actionID.Hex() + "?submissionTag=submit"

	var result *http.Response
	var err error
	for range 15 {
		result, err = http.Get(url)
		if err == nil && result.StatusCode == http.StatusOK {
			break
		}
		if result != nil {
			result.Body.Close()
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, errors.Errorf("polling %s: %s", url, err)
	}
	if result.StatusCode != http.StatusOK {
		return nil, errors.Errorf("action result status not ok, got: %d", result.StatusCode)
	}
	defer result.Body.Close()

	var response types.ActionResponse
	if err := json.NewDecoder(result.Body).Decode(&response); err != nil {
		return nil, errors.Errorf("decoding response: %s", err)
	}
	return &response, nil
}
