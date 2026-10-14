package db

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/require"
)

func staticIPTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/test.db")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	for _, vmid := range []int{1201, 1202, 1203} {
		now := time.Now().UTC()
		require.NoError(t, s.CreateSandbox(context.Background(), models.Sandbox{VMID: vmid, Name: "test", Profile: "default", State: models.SandboxRequested, CreatedAt: now, LastUpdatedAt: now}))
	}
	return s
}

func TestReserveSandboxStaticIPs(t *testing.T) {
	ctx := context.Background()
	s := staticIPTestStore(t)
	require.NoError(t, s.ReserveSandboxStaticIPs(ctx, 1201, []string{"10.77.0.22", "2001:db8::22"}))
	require.NoError(t, s.ReserveSandboxStaticIPs(ctx, 1201, []string{"10.77.0.22"}))
	// A conflict rolls back the entire claim, including the first free address.
	require.ErrorContains(t, s.ReserveSandboxStaticIPs(ctx, 1202, []string{"10.77.0.23", "2001:0db8:0:0:0:0:0:22"}), "sandbox 1201")
	require.NoError(t, s.ReserveSandboxStaticIPs(ctx, 1203, []string{"10.77.0.23"}))
	// Reopening and stopping must not free an address that the VM will reuse.
	require.NoError(t, s.Close())
	reopened, err := Open(s.Path)
	require.NoError(t, err)
	defer reopened.Close()
	_, err = reopened.DB.Exec("UPDATE sandboxes SET state = 'STOPPED' WHERE vmid = 1201")
	require.NoError(t, err)
	require.ErrorContains(t, reopened.ReserveSandboxStaticIPs(ctx, 1202, []string{"10.77.0.22"}), "sandbox 1201")
	_, err = reopened.DB.Exec("UPDATE sandboxes SET state = 'DESTROYED' WHERE vmid = 1201")
	require.NoError(t, err)
	require.NoError(t, reopened.ReserveSandboxStaticIPs(ctx, 1202, []string{"10.77.0.22", "2001:db8::22"}))
	require.NoError(t, reopened.ReleaseSandboxStaticIPs(ctx, 1202))
	require.NoError(t, reopened.ReserveSandboxStaticIPs(ctx, 1203, []string{"10.77.0.22"}))
}

func TestReserveSandboxStaticIPsObservedAddress(t *testing.T) {
	s := staticIPTestStore(t)
	ctx := context.Background()
	require.NoError(t, s.UpdateSandboxIP(ctx, 1201, "10.77.0.22"))
	require.ErrorContains(t, s.ReserveSandboxStaticIPs(ctx, 1202, []string{"10.77.0.22"}), "sandbox 1201")
}

func TestReserveSandboxStaticIPsConcurrent(t *testing.T) {
	s := staticIPTestStore(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, vmid := range []int{1201, 1202} {
		wg.Add(1)
		go func(vmid int) {
			defer wg.Done()
			results <- s.ReserveSandboxStaticIPs(context.Background(), vmid, []string{"10.77.0.22"})
		}(vmid)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorContains(t, err, "already reserved")
		}
	}
	require.Equal(t, 1, successes)
}
