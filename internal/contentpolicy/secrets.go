package contentpolicy

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// ReadSecrets reads bounded, NUL-delimited values from a private regular file.
// Errors never contain credential values.
func ReadSecrets(filename string) ([][]byte, error) {
	if filename == "" {
		return nil, nil
	}
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return nil, fmt.Errorf("credential file must be private, regular, and bounded")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("credential file is unavailable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, fmt.Errorf("credential file is unavailable or oversized")
	}
	values := bytes.Split(data, []byte{0})
	if len(values) > 1024 {
		return nil, fmt.Errorf("credential file has too many values")
	}
	return values, nil
}

// ContainsSecret checks exact known values, including binary and multiline data.
func ContainsSecret(data []byte, values [][]byte) bool {
	for _, value := range values {
		if len(value) > 0 && bytes.Contains(data, value) {
			return true
		}
	}
	return false
}
