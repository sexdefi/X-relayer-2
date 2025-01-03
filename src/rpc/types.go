package rpc

import (
	"math/big"
)

// Block 区块数据结构
type Block struct {
	Number       uint64         `json:"number"`
	Hash         string         `json:"hash"`
	ParentHash   string         `json:"parentHash"`
	Time         uint64         `json:"timestamp"`
	Transactions []*Transaction `json:"transactions"`
}

// Transaction 交易数据结构
type Transaction struct {
	Hash     string   `json:"hash"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Value    *big.Int `json:"value"`
	Status   uint64   `json:"status"`
	Logs     []*Log   `json:"logs"`
	GasPrice *big.Int `json:"gasPrice"`
	Gas      uint64   `json:"gas"`
}

// Log 事件日志数据结构
type Log struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    []byte   `json:"data"`
}
