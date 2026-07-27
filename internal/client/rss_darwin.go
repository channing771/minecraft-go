//go:build darwin

package client

import "syscall"

// ProcessRSSBytes 返回内核记录的本进程峰值常驻内存。
// Darwin 的 ru_maxrss 单位是字节。
func ProcessRSSBytes() (uint64, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, err
	}
	return uint64(usage.Maxrss), nil
}
