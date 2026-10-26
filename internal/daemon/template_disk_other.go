//go:build !linux

package daemon

import "fmt"

func templateHeadroom(path string, need int64) error { return fmt.Errorf("linux host required") }
