package models

import (
	"time"
)

// Transaction 交易信息
type Transaction struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement"`
	BlockNumber uint64    `gorm:"index;not null"`
	TxHash      string    `gorm:"type:char(66);uniqueIndex;not null"`
	FromAddr    string    `gorm:"type:char(42);index;not null"`
	ToAddr      string    `gorm:"type:char(42);index;not null"`
	Value       string    `gorm:"type:varchar(78);not null"` // 最大256位
	Status      uint64    `gorm:"not null"`                  // 1成功 0失败
	CreatedAt   time.Time `gorm:"not null"`
}

// TableName 指定表名
func (Transaction) TableName() string {
	return "transactions"
}
