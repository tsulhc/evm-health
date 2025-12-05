package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexallah/ethereum-healthmon/internal/common"
)

var blockTrack common.BlockTrack

func isReady(addr, token string, timeout int64, syncTolerance uint64) error {
	// is syncing?
	syncInfo, err := getSyncing(addr, token, timeout)
	if err != nil {
		return err
	}

	if syncInfo != nil {
		dist := syncInfo.distance()

		if dist > syncTolerance {
			return fmt.Errorf("syncing, distance %d blocks (tolerance: %d)", dist, syncTolerance)
		}

		if dist > 0 {
			log.Printf("Node is syncing but within tolerance (%d <= %d). Checking block age...", dist, syncTolerance)
		}
	}

	// get latest block info
	block, err := getLatestBlock(addr, token, timeout)
	if err != nil {
		return err
	}
	blockNumber, err := block.number()
	if err != nil {
		return err
	}
	blockTrack.AddBlock(blockNumber)
	// make sure it is recent enough
	if err := block.checkAge(); err != nil {
		return err
	}

	// wait for a new block
	if err := blockTrack.HealthCheck(); err != nil {
		return err
	}

	return nil
}

// json unmarshal helpers
type SyncInfo struct {
	CurrentBlockHex string `json:"currentBlock"`
	HighestBlockHex string `json:"highestBlock"`
}

func (s *SyncInfo) currentBlock() uint64 {
	return parseUintFromHex(s.CurrentBlockHex)
}

func (s *SyncInfo) highestBlock() uint64 {
	return parseUintFromHex(s.HighestBlockHex)
}

func (s *SyncInfo) distance() uint64 {
	h := s.highestBlock()
	c := s.currentBlock()

	// Protezione contro underflow se highestBlock è 0 (parsing fallito o campo mancante)
	// o se il nodo riporta dati incoerenti temporanei.
	if h < c {
		return 0
	}
	return h - c
}

// json unmarshal helper
type JsonResultSync struct {
	Result SyncInfo `json:"result"`
}

type JsonResultBool struct {
	Result bool `json:"result"`
}

type JsonResultString struct {
	Result string `json:"result"`
}

type Block struct {
	Timestamp string `json:"timestamp"`
	Number    string `json:"number"`
}

func (b *Block) time() (time.Time, error) {
	timeHex := strings.TrimLeft(b.Timestamp, "0x")
	unixtime, err := strconv.ParseInt(timeHex, 16, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("can not parse block timestamp: %w", err)
	}
	return time.Unix(unixtime, 0), nil
}

func (b *Block) number() (uint64, error) {
	numHex := strings.TrimLeft(b.Number, "0x")
	blockNumber, err := strconv.ParseUint(numHex, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("can not parse block number: %w", err)
	}
	return blockNumber, nil
}

func (b *Block) checkAge() error {
	created, err := b.time()
	if err != nil {
		return err
	}
	age := time.Since(created)
	if age > 300*time.Second {
		return fmt.Errorf("latest block is too old: %s", age.Truncate(time.Second))
	}
	return nil
}

type JsonResultBlock struct {
	Result Block `json:"result"`
}

// execute an RPC request and return true if the server is synced and ready
func getSyncing(addr, token string, timeout int64) (*SyncInfo, error) {
	payload := []byte(`{"jsonrpc":"2.0","method":"eth_syncing","params":[],"id":1}`)
	body, err := request(addr, token, timeout, payload)
	if err != nil {
		return nil, err
	}

	resultSync := new(JsonResultSync)
	err = json.Unmarshal(body, resultSync)
	if err == nil {
		return &resultSync.Result, nil
	}

	// try parsing as bool
	resultBool := new(JsonResultBool)
	if err := json.Unmarshal(body, resultBool); err != nil {
		return nil, err
	}
	if resultBool.Result {
		return nil, errors.New("syncing is true, not expected")
	}
	return nil, nil
}

// parseUintFromHex parses a hex string safely without panicking
func parseUintFromHex(hex string) uint64 {
	if hex == "" {
		return 0
	}
	trimmed := strings.TrimPrefix(hex, "0x")
	if trimmed == "" {
		return 0
	}
	result, err := strconv.ParseUint(trimmed, 16, 64)
	if err != nil {
		// Log warning instead of panic to prevent service crash on malformed/empty responses
		log.Printf("warning: error parsing hex '%v': %v. Defaulting to 0.", hex, err)
		return 0
	}
	return result
}

func getLatestBlock(addr, token string, timeout int64) (*Block, error) {
	payload := []byte(`{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["latest",false],"id":1}`)
	body, err := request(addr, token, timeout, payload)
	if err != nil {
		return nil, err
	}

	result := new(JsonResultBlock)
	if err := json.Unmarshal(body, result); err != nil {
		return nil, err
	}

	return &result.Result, nil
}

func request(addr, token string, timeout int64, payload []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", addr, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}

	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("incorrect response status code: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
