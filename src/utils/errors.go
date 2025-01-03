package utils

import (
	"fmt"
)

// 定义错误类型
var (
	ErrRPCFailed     = fmt.Errorf("RPC请求失败")
	ErrBlockNotFound = fmt.Errorf("区块未找到")
	ErrDBFailed      = fmt.Errorf("数据库操作失败")
	ErrCacheFailed   = fmt.Errorf("缓存操作失败")
)

// WrapError 包装错误信息
func WrapError(err error, msg string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", msg, err)
}
