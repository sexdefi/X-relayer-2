package models

import (
	"time"
)

const (
	TxTypeNative  = "native"  // 主币转账
	TxTypeERC20   = "erc20"   // ERC20代币转账
	TxTypeUnknown = "unknown" // 其他类型交易
)

// Transaction 交易信息
type Transaction struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	BlockNumber uint64 `gorm:"index:idx_block_number"`
	TxHash      string `gorm:"uniqueIndex;type:varchar(66)"`
	TxType      string `gorm:"type:varchar(20);index"` // 交易类型
	FromAddr    string `gorm:"type:varchar(42);index"`
	ToAddr      string `gorm:"type:varchar(42);index"`
	TokenAddr   string `gorm:"type:varchar(42);index"` // 代币地址（如果是代币转账）
	Value       string `gorm:"type:varchar(78)"`       // 转账金额
	Status      uint64 `gorm:"type:tinyint"`
	Input       string `gorm:"type:text"` // 交易输入数据
	CreatedAt   time.Time
}

// TableName 指定表名
func (Transaction) TableName() string {
	return "transactions"
}
