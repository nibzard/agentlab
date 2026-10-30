package connection

import (
	"context"
	"errors"
	"os"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

func openPlatformCredentialVault(ctx context.Context, root string) (CredentialVault, error) {
	return newKeychainVault(ctx, root, runKeychainWorker)
}

func init() {
	if os.Getenv(keychainWorkerMarker) != "1" || len(os.Args) != 2 || os.Args[1] != keychainWorkerArgument {
		return
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}); err != nil {
		os.Exit(1)
	}
	// The parent receives only the versioned response. Native diagnostics and panic
	// stacks never become CLI output. The parent imposes a hard process deadline.
	err := serveKeychainWorker(os.Stdin, os.Stdout, func(request keychainRequest) keychainResponse {
		api, err := loadKeychainNative()
		if err != nil {
			return keychainResponse{Version: 1, Operation: request.Operation, Scope: request.Scope, Reference: request.Reference, Status: "unavailable"}
		}
		return api.handle(request)
	})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func loadKeychainNative() (*keychainNative, error) {
	security, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, errors.New("Security framework unavailable")
	}
	core, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, errors.New("Core Foundation unavailable")
	}
	system, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, errors.New("system library unavailable")
	}
	// Global CF references are exported pointer values. Copy the value through the
	// native ABI instead of converting arbitrary integer addresses to Go pointers.
	var copyPointer func(*uintptr, uintptr, uintptr) uintptr
	address, err := purego.Dlsym(system, "memcpy")
	if err != nil {
		return nil, err
	}
	purego.RegisterFunc(&copyPointer, address)
	api := &keychainNative{constants: map[string]uintptr{}}
	for name, target := range map[string]any{
		"SecKeychainSetUserInteractionAllowed": &api.setInteractionAllowed,
		"SecKeychainCopyDefault":               &api.copyDefault, "SecKeychainGetStatus": &api.getStatus,
		"SecItemAdd": &api.itemAdd, "SecItemCopyMatching": &api.itemCopyMatching, "SecItemDelete": &api.itemDelete,
	} {
		address, err := purego.Dlsym(security, name)
		if err != nil {
			return nil, err
		}
		purego.RegisterFunc(target, address)
	}
	for name, target := range map[string]any{
		"CFRelease": &api.release, "CFStringCreateWithBytes": &api.stringCreate, "CFDataCreate": &api.dataCreate,
		"CFDictionaryCreateMutable": &api.dictionaryCreate, "CFDictionarySetValue": &api.dictionarySet,
		"CFArrayCreate": &api.arrayCreate, "CFArrayGetCount": &api.arrayCount, "CFArrayGetValueAtIndex": &api.arrayValue,
		"CFGetTypeID": &api.getTypeID, "CFArrayGetTypeID": &api.arrayTypeID, "CFDataGetTypeID": &api.dataTypeID,
		"CFDataGetLength": &api.dataLength, "CFDataGetBytePtr": &api.dataPointer,
	} {
		address, err := purego.Dlsym(core, name)
		if err != nil {
			return nil, err
		}
		purego.RegisterFunc(target, address)
	}
	for _, name := range []string{"kSecClass", "kSecClassGenericPassword", "kSecAttrService", "kSecAttrAccount", "kSecAttrLabel", "kSecValueData", "kSecUseKeychain", "kSecMatchSearchList", "kSecReturnData", "kSecMatchLimit", "kSecMatchLimitAll", "kSecUseAuthenticationUI", "kSecUseAuthenticationUIFail", "kSecUseDataProtectionKeychain", "kSecAttrSynchronizable", "kCFBooleanTrue", "kCFBooleanFalse"} {
		library := security
		if name == "kCFBooleanTrue" || name == "kCFBooleanFalse" {
			library = core
		}
		address, err := purego.Dlsym(library, name)
		if err != nil {
			return nil, err
		}
		var value uintptr
		copyPointer(&value, address, unsafe.Sizeof(value))
		if value == 0 {
			return nil, errors.New("missing Keychain constant")
		}
		api.constants[name] = value
	}
	// Dictionary key and value callbacks have different structures and symbols.
	api.dictionaryKeyCallbacks, err = purego.Dlsym(core, "kCFTypeDictionaryKeyCallBacks")
	if err != nil {
		return nil, err
	}
	api.dictionaryValueCallbacks, err = purego.Dlsym(core, "kCFTypeDictionaryValueCallBacks")
	if err != nil {
		return nil, err
	}
	api.arrayCallbacks, err = purego.Dlsym(core, "kCFTypeArrayCallBacks")
	if err != nil {
		return nil, err
	}
	return api, nil
}
