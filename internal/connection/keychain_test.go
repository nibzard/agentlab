package connection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
)

type keychainFixture struct {
	api         *keychainNative
	next        uintptr
	values      map[uintptr]any
	records     map[string][]byte
	queries     []map[string]any
	access      uint32
	interaction bool
	addStatus   int32
	readCount   int
}

func newKeychainFixture(t *testing.T) *keychainFixture {
	t.Helper()
	f := &keychainFixture{next: 10000, values: map[uintptr]any{}, records: map[string][]byte{}, access: 7, interaction: true, readCount: 1}
	create := func(value any) uintptr { f.next++; f.values[f.next] = value; return f.next }
	api := &keychainNative{constants: map[string]uintptr{}, dictionaryKeyCallbacks: 9001, dictionaryValueCallbacks: 9002, arrayCallbacks: 9003}
	f.api = api
	for _, name := range []string{"kSecClass", "kSecClassGenericPassword", "kSecAttrService", "kSecAttrAccount", "kSecAttrLabel", "kSecValueData", "kSecUseKeychain", "kSecMatchSearchList", "kSecReturnData", "kSecMatchLimit", "kSecMatchLimitAll", "kSecUseAuthenticationUI", "kSecUseAuthenticationUIFail", "kSecUseDataProtectionKeychain", "kSecAttrSynchronizable", "kCFBooleanTrue", "kCFBooleanFalse"} {
		api.constants[name] = create(name)
	}
	api.setInteractionAllowed = func(allowed bool) int32 { require.False(t, allowed); f.interaction = allowed; return 0 }
	api.copyDefault = func(out *uintptr) int32 { require.False(t, f.interaction); *out = 8; return 0 }
	api.getStatus = func(keychain uintptr, out *uint32) int32 {
		require.Equal(t, uintptr(8), keychain)
		*out = f.access
		return 0
	}
	api.release = func(ref uintptr) { require.NotZero(t, ref) }
	api.stringCreate = func(_ uintptr, data *byte, length int, encoding uint32, external bool) uintptr {
		require.Equal(t, uint32(0x08000100), encoding)
		require.False(t, external)
		return create(string(unsafe.Slice(data, length)))
	}
	api.dataCreate = func(_ uintptr, data *byte, length int) uintptr {
		return create(bytes.Clone(unsafe.Slice(data, length)))
	}
	api.dictionaryCreate = func(_ uintptr, _ int, key, value uintptr) uintptr {
		require.Equal(t, uintptr(9001), key)
		require.Equal(t, uintptr(9002), value)
		return create(map[string]any{})
	}
	api.dictionarySet = func(dictionary, key, value uintptr) {
		name := f.values[key].(string)
		if decoded, ok := f.values[value]; ok {
			f.values[dictionary].(map[string]any)[name] = decoded
		} else {
			f.values[dictionary].(map[string]any)[name] = value
		}
	}
	api.arrayCreate = func(_ uintptr, items *uintptr, count int, callbacks uintptr) uintptr {
		require.Equal(t, uintptr(9003), callbacks)
		require.Equal(t, 1, count)
		return create([]uintptr{*items})
	}
	checkQuery := func(ref uintptr) (string, map[string]any) {
		query := f.values[ref].(map[string]any)
		f.queries = append(f.queries, query)
		require.False(t, f.interaction)
		require.Equal(t, "kSecClassGenericPassword", query["kSecClass"])
		require.Equal(t, "kSecUseAuthenticationUIFail", query["kSecUseAuthenticationUI"])
		require.Equal(t, "kCFBooleanFalse", query["kSecUseDataProtectionKeychain"])
		require.Equal(t, "kCFBooleanFalse", query["kSecAttrSynchronizable"])
		return query["kSecAttrService"].(string) + "/" + query["kSecAttrAccount"].(string), query
	}
	api.itemAdd = func(ref uintptr, out *uintptr) int32 {
		require.Nil(t, out)
		key, query := checkQuery(ref)
		require.Equal(t, uintptr(8), query["kSecUseKeychain"])
		require.NotContains(t, query, "kSecMatchSearchList")
		if f.addStatus != 0 {
			return f.addStatus
		}
		if _, ok := f.records[key]; ok {
			return -25299
		}
		f.records[key] = bytes.Clone(query["kSecValueData"].([]byte))
		return 0
	}
	api.itemCopyMatching = func(ref uintptr, out *uintptr) int32 {
		key, query := checkQuery(ref)
		require.Equal(t, []uintptr{8}, query["kSecMatchSearchList"])
		require.Equal(t, "kSecMatchLimitAll", query["kSecMatchLimit"])
		require.Equal(t, "kCFBooleanTrue", query["kSecReturnData"])
		data, ok := f.records[key]
		if !ok {
			return -25300
		}
		values := []uintptr{}
		for i := 0; i < f.readCount; i++ {
			values = append(values, create(bytes.Clone(data)))
		}
		*out = create(values)
		return 0
	}
	api.itemDelete = func(ref uintptr) int32 {
		key, query := checkQuery(ref)
		require.Equal(t, []uintptr{8}, query["kSecMatchSearchList"])
		require.NotContains(t, query, "kSecReturnData")
		if _, ok := f.records[key]; !ok {
			return -25300
		}
		delete(f.records, key)
		return 0
	}
	api.getTypeID = func(ref uintptr) uintptr {
		switch f.values[ref].(type) {
		case []uintptr:
			return 20
		case []byte:
			return 21
		default:
			return 99
		}
	}
	api.arrayTypeID = func() uintptr { return 20 }
	api.dataTypeID = func() uintptr { return 21 }
	api.arrayCount = func(ref uintptr) int { return len(f.values[ref].([]uintptr)) }
	api.arrayValue = func(ref uintptr, index int) uintptr { return f.values[ref].([]uintptr)[index] }
	api.dataLength = func(ref uintptr) int { return len(f.values[ref].([]byte)) }
	api.dataPointer = func(ref uintptr) unsafe.Pointer { return unsafe.Pointer(&f.values[ref].([]byte)[0]) }
	return f
}
func (f *keychainFixture) run(ctx context.Context, input []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	err := serveKeychainWorker(bytes.NewReader(input), &output, f.api.handle)
	return output.Bytes(), err
}

func TestKeychainQueriesAndConfigurationIsolation(t *testing.T) {
	fixture := newKeychainFixture(t)
	ctx := context.Background()
	first, err := newKeychainVault(ctx, t.TempDir(), fixture.run)
	require.NoError(t, err)
	second, err := newKeychainVault(ctx, t.TempDir(), fixture.run)
	require.NoError(t, err)
	ref := strings.Repeat("a", 32)
	data := []byte(`{"token":"private-token"}`)
	require.NoError(t, first.Save(ctx, ref, data))
	actual, err := first.Load(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, data, actual)
	require.Error(t, first.Save(ctx, ref, []byte("replacement")))
	_, err = second.Load(ctx, ref)
	require.Error(t, err)
	actual, err = first.Load(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, data, actual)
	require.NoError(t, second.Delete(ctx, ref))
	require.Len(t, fixture.records, 1)
	fixture.readCount = 2
	_, err = first.Load(ctx, ref)
	require.ErrorContains(t, err, "duplicate")
	fixture.readCount = 1
	require.NoError(t, first.Delete(ctx, ref))
	require.Empty(t, fixture.records)
	require.NoError(t, first.Delete(ctx, ref))
	require.True(t, strings.HasPrefix(fixture.queries[0]["kSecAttrService"].(string), "io.agentlab.credentials."))
}
func TestKeychainLockedAndDeniedNeverMutate(t *testing.T) {
	for _, access := range []uint32{0, 1, 2, 3} {
		t.Run(fmt.Sprint(access), func(t *testing.T) {
			fixture := newKeychainFixture(t)
			fixture.access = access
			vault := keychainVault{scope: strings.Repeat("b", 64), run: fixture.run}
			require.Error(t, vault.Save(context.Background(), strings.Repeat("a", 32), []byte("credential")))
			require.Empty(t, fixture.queries)
			require.Empty(t, fixture.records)
		})
	}
	fixture := newKeychainFixture(t)
	fixture.addStatus = -25293
	vault := keychainVault{scope: strings.Repeat("b", 64), run: fixture.run}
	err := vault.Save(context.Background(), strings.Repeat("a", 32), []byte("credential"))
	var failure *CredentialError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "credential_store_denied", failure.Code)
}
func TestKeychainContextReplacementAndJournalRecovery(t *testing.T) {
	fixture := newKeychainFixture(t)
	root := t.TempDir()
	vault, err := newKeychainVault(context.Background(), root, fixture.run)
	require.NoError(t, err)
	store := ContextStore{Root: root, CredentialMode: "os", Vault: vault}
	cfg := Settings{Endpoint: "https://fixture.example", Token: "private-token", ServerIdentity: "fixture"}
	require.NoError(t, store.Put("server", cfg))
	items, _, err := store.List()
	require.NoError(t, err)
	require.Equal(t, "macos-keychain", items[0].CredentialStorage)
	raw, err := os.ReadFile(filepath.Join(root, "contexts.json"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), cfg.Token)
	actual, _, _, err := store.Resolve("server")
	require.NoError(t, err)
	require.Equal(t, cfg, actual)
	intent := interruptedCredential(t, store, "other", cfg, false)
	require.Equal(t, "macos-keychain", intent.CredentialStorage)
	require.Len(t, fixture.records, 2)
	pending, err := store.PendingCredentials()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.NoError(t, store.CleanupCredentials())
	require.Len(t, fixture.records, 1)
	require.NoError(t, store.Remove("server"))
	require.Empty(t, fixture.records)
}
func TestKeychainProtocolRejectsUnboundOrUnboundedRecords(t *testing.T) {
	fixture := newKeychainFixture(t)
	request := keychainRequest{Version: 1, Operation: "load", Scope: strings.Repeat("a", 64), Reference: strings.Repeat("b", 32)}
	valid := keychainResponse{Version: 1, Operation: request.Operation, Scope: request.Scope, Reference: request.Reference, Status: "ok", Data: []byte("private-token")}
	for _, mode := range []string{"scope", "reference", "version", "operation", "extra", "size", "failed-data"} {
		t.Run(mode, func(t *testing.T) {
			response := valid
			switch mode {
			case "scope":
				response.Scope = strings.Repeat("c", 64)
			case "reference":
				response.Reference = strings.Repeat("c", 32)
			case "version":
				response.Version = 2
			case "operation":
				response.Operation = "save"
			case "size":
				response.Data = bytes.Repeat([]byte("x"), maxCredentialBytes+1)
			case "failed-data":
				response.Status = "locked"
			}
			output, _ := json.Marshal(response)
			if mode == "extra" {
				output = append(output, []byte(" {}")...)
			}
			vault := keychainVault{scope: request.Scope, run: func(context.Context, []byte) ([]byte, error) { return output, nil }}
			_, err := vault.Load(context.Background(), request.Reference)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-token")
		})
	}
	for _, raw := range []string{`{}`, `{"version":1,"operation":"unlock"}`, strings.Repeat("x", maxKeychainMessage+1)} {
		var output bytes.Buffer
		err := serveKeychainWorker(strings.NewReader(raw), &output, fixture.api.handle)
		require.Error(t, err)
		require.Empty(t, output.String())
	}
}
func TestKeychainWorkerProcessBoundsAndPrivacy(t *testing.T) {
	if os.Getenv("AGENTLAB_TEST_KEYCHAIN_PROCESS") == "1" {
		switch os.Getenv("AGENTLAB_TEST_KEYCHAIN_MODE") {
		case "wait":
			time.Sleep(time.Minute)
		case "flood":
			fmt.Print(strings.Repeat("x", maxKeychainMessage+1))
		case "error":
			fmt.Fprint(os.Stderr, "private-token")
			os.Exit(2)
		}
		os.Exit(0)
	}
	for _, mode := range []string{"wait", "flood", "error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKeychainWorkerProcessBoundsAndPrivacy$")
			command.Env = append(os.Environ(), "AGENTLAB_TEST_KEYCHAIN_PROCESS=1", "AGENTLAB_TEST_KEYCHAIN_MODE="+mode)
			started := time.Now()
			output, err := runKeychainProcess(command, []byte("private-token"))
			require.Error(t, err)
			require.Empty(t, output)
			require.NotContains(t, err.Error(), "private-token")
			require.Less(t, time.Since(started), 3*time.Second)
			require.NotNil(t, command.ProcessState)
			require.True(t, command.ProcessState.Exited() || command.ProcessState.Sys().(interface{ Signaled() bool }).Signaled())
		})
	}
	t.Setenv("DYLD_INSERT_LIBRARIES", "untrusted")
	t.Setenv("AGENTLAB_TOKEN", "private-token")
	command, err := keychainWorkerCommand(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{command.Path, keychainWorkerArgument}, command.Args)
	require.NotContains(t, strings.Join(command.Env, "\n"), "private-token")
	require.NotContains(t, strings.Join(command.Env, "\n"), "DYLD_")
	require.Nil(t, command.Stdin)
}
func TestKeychainUnavailableAndExistingReferencesDoNotFallback(t *testing.T) {
	run := func(_ context.Context, input []byte) ([]byte, error) {
		var request keychainRequest
		json.Unmarshal(input, &request)
		return json.Marshal(keychainResponse{Version: 1, Operation: request.Operation, Scope: request.Scope, Reference: request.Reference, Status: "unavailable"})
	}
	_, err := newKeychainVault(context.Background(), t.TempDir(), run)
	require.ErrorIs(t, err, errCredentialStoreUnavailable)
	vault := keychainVault{scope: strings.Repeat("a", 64), run: run}
	_, err = vault.Load(context.Background(), strings.Repeat("b", 32))
	require.Error(t, err)
	require.False(t, errors.Is(err, errCredentialStoreUnavailable))
	store := ContextStore{Root: t.TempDir(), Vault: &memoryVault{}}
	_, err = store.readVault(context.Background(), "macos-keychain")
	require.Error(t, err)
}

func TestKeychainNativeRejectsInvalidCoreFoundationResults(t *testing.T) {
	for _, mode := range []string{"wrong-array", "empty", "duplicate", "wrong-data", "oversize", "nil-pointer"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newKeychainFixture(t)
			ctx := context.Background()
			vault := keychainVault{scope: strings.Repeat("a", 64), run: fixture.run}
			ref := strings.Repeat("b", 32)
			require.NoError(t, vault.Save(ctx, ref, []byte("native-record")))
			switch mode {
			case "wrong-array":
				fixture.api.getTypeID = func(uintptr) uintptr { return 99 }
			case "empty":
				fixture.readCount = 0
			case "duplicate":
				fixture.readCount = 2
			case "wrong-data":
				original := fixture.api.getTypeID
				fixture.api.getTypeID = func(value uintptr) uintptr {
					if original(value) == 21 {
						return 99
					}
					return original(value)
				}
			case "oversize":
				fixture.api.dataLength = func(uintptr) int { return maxCredentialBytes + 1 }
				fixture.api.dataPointer = func(uintptr) unsafe.Pointer { t.Fatal("unbounded native data was read"); return nil }
			case "nil-pointer":
				fixture.api.dataPointer = func(uintptr) unsafe.Pointer { return nil }
			}
			data, err := vault.Load(ctx, ref)
			require.Error(t, err)
			require.Empty(t, data)
			require.Len(t, fixture.records, 1)
		})
	}
}
