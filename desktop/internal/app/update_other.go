//go:build !windows

package app

import "fmt"

func clientUpdateSupported() bool                   { return false }
func currentUpdateProcessIdentity() (uint64, error) { return 0, fmt.Errorf("仅支持 Windows") }
func prepareUpdateWait(clientUpdatePlan) (func() error, error) {
	return nil, fmt.Errorf("仅支持 Windows")
}
func installUpdatedClient(string, string) error { return fmt.Errorf("仅支持 Windows") }
