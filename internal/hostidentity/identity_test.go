package hostidentity

import (
	"strings"
	"testing"
)

func TestIdentityIsStableAndDoesNotExposeMachineID(t *testing.T) {
	const raw = "0123456789abcdef0123456789abcdef"
	id, err := FromMachineID(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := FromMachineID(raw + "\n")
	if err != nil || again != id {
		t.Fatal("identity changed")
	}
	if strings.Contains(id, raw) || !strings.HasPrefix(id, "server_") {
		t.Fatal("raw machine identity exposed")
	}
	other, err := FromMachineID("1123456789abcdef0123456789abcdef")
	if err != nil || other == id {
		t.Fatal("different servers share an identity")
	}
	if _, err := FromMachineID("invalid"); err == nil {
		t.Fatal("invalid machine identity accepted")
	}
}
