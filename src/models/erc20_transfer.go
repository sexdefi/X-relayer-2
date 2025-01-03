package models

import (
	"time"
)

// ERC20Transfer ERC20代币转账记录
type ERC20Transfer struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	BlockNumber uint64 `gorm:"index:idx_block_number"`
	TxHash      string `gorm:"index;type:varchar(66)"`
	TokenAddr   string `gorm:"index;type:varchar(42)"` // 代币合约地址
	FromAddr    string `gorm:"index;type:varchar(42)"`
	ToAddr      string `gorm:"index;type:varchar(42)"`
	Value       string `gorm:"type:varchar(78)"` // 转账金额
	MethodID    string `gorm:"type:varchar(10)"` // 方法ID (transfer/transferFrom)
	CreatedAt   time.Time
}

// TableName 指定表名
func (ERC20Transfer) TableName() string {
	return "erc20_transfers"
}
