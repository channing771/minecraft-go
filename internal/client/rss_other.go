//go:build !darwin

package client

func ProcessRSSBytes() (uint64, error) {
	return 0, ErrRSSUnsupported
}
