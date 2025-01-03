package utils

import (
	"regexp"
)

var addressRegex = regexp.MustCompile("^0x[0-9a-fA-F]{40}$")

// IsValidAddress 验证以太坊地址格式
func IsValidAddress(address string) bool {
	return addressRegex.MatchString(address)
}
