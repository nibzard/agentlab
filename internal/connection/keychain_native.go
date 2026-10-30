package connection

import (
	"bytes"
	"runtime"
	"unsafe"
)

// These signatures use the 64-bit Core Foundation and Security C interfaces.
// Only the macOS worker binds them. Portable fixtures inspect the same queries.
type keychainNative struct {
	constants                map[string]uintptr
	dictionaryKeyCallbacks   uintptr
	dictionaryValueCallbacks uintptr
	arrayCallbacks           uintptr
	setInteractionAllowed    func(bool) int32
	copyDefault              func(*uintptr) int32
	getStatus                func(uintptr, *uint32) int32
	itemAdd                  func(uintptr, *uintptr) int32
	itemCopyMatching         func(uintptr, *uintptr) int32
	itemDelete               func(uintptr) int32
	release                  func(uintptr)
	stringCreate             func(uintptr, *byte, int, uint32, bool) uintptr
	dataCreate               func(uintptr, *byte, int) uintptr
	dictionaryCreate         func(uintptr, int, uintptr, uintptr) uintptr
	dictionarySet            func(uintptr, uintptr, uintptr)
	arrayCreate              func(uintptr, *uintptr, int, uintptr) uintptr
	arrayCount               func(uintptr) int
	arrayValue               func(uintptr, int) uintptr
	getTypeID                func(uintptr) uintptr
	arrayTypeID              func() uintptr
	dataTypeID               func() uintptr
	dataLength               func(uintptr) int
	dataPointer              func(uintptr) unsafe.Pointer
}

func keychainNativeStatus(status int32) string {
	switch status {
	case 0:
		return "ok"
	case -25291, -25307:
		return "unavailable" // errSecNotAvailable, errSecNoDefaultKeychain
	case -25308, -25315:
		return "locked" // interaction is disallowed or required
	case -25293:
		return "denied" // errSecAuthFailed
	case -25300:
		return "not_found"
	case -25299:
		return "exists"
	default:
		return "failed"
	}
}
func (api *keychainNative) handle(request keychainRequest) keychainResponse {
	response := keychainResponse{Version: 1, Operation: request.Operation, Scope: request.Scope, Reference: request.Reference, Status: "failed"}
	if !validKeychainRequest(request) {
		return response
	}
	// This worker never reenables interaction and exits after one operation.
	// The legacy Keychain ignores the SecItem query UI flag in some paths.
	if api.setInteractionAllowed(false) != 0 {
		response.Status = "denied"
		return response
	}
	var keychain uintptr
	status := api.copyDefault(&keychain)
	if keychain != 0 {
		defer api.release(keychain)
	}
	if status != 0 {
		response.Status = keychainNativeStatus(status)
		return response
	}
	if keychain == 0 {
		return response
	}
	var access uint32
	if status = api.getStatus(keychain, &access); status != 0 {
		response.Status = keychainNativeStatus(status)
		return response
	}
	if access&1 == 0 {
		response.Status = "locked"
		return response
	}
	if access&2 == 0 || ((request.Operation == "save" || request.Operation == "delete") && access&4 == 0) {
		response.Status = "denied"
		return response
	}
	if request.Operation == "probe" {
		response.Status = "ok"
		return response
	}
	query := api.dictionaryCreate(0, 0, api.dictionaryKeyCallbacks, api.dictionaryValueCallbacks)
	if query == 0 {
		return response
	}
	defer api.release(query)
	set := func(name string, value uintptr) { api.dictionarySet(query, api.constants[name], value) }
	set("kSecClass", api.constants["kSecClassGenericPassword"])
	set("kSecUseAuthenticationUI", api.constants["kSecUseAuthenticationUIFail"])
	set("kSecUseDataProtectionKeychain", api.constants["kCFBooleanFalse"])
	set("kSecAttrSynchronizable", api.constants["kCFBooleanFalse"])
	text := func(value string) uintptr {
		data := []byte(value)
		created := api.stringCreate(0, &data[0], len(data), 0x08000100, false)
		runtime.KeepAlive(data)
		return created
	}
	service := text("io.agentlab.credentials." + request.Scope)
	if service == 0 {
		return response
	}
	defer api.release(service)
	account := text(request.Reference)
	if account == 0 {
		return response
	}
	defer api.release(account)
	set("kSecAttrService", service)
	set("kSecAttrAccount", account)
	if request.Operation == "save" {
		set("kSecUseKeychain", keychain)
		label := text("AgentLab connection")
		if label == 0 {
			return response
		}
		defer api.release(label)
		set("kSecAttrLabel", label)
		data := api.dataCreate(0, &request.Data[0], len(request.Data))
		runtime.KeepAlive(request.Data)
		if data == 0 {
			return response
		}
		defer api.release(data)
		set("kSecValueData", data)
		response.Status = keychainNativeStatus(api.itemAdd(query, nil))
		return response
	}
	search := api.arrayCreate(0, &keychain, 1, api.arrayCallbacks)
	runtime.KeepAlive(keychain)
	if search == 0 {
		return response
	}
	defer api.release(search)
	set("kSecMatchSearchList", search)
	if request.Operation == "delete" {
		response.Status = keychainNativeStatus(api.itemDelete(query))
		return response
	}
	set("kSecReturnData", api.constants["kCFBooleanTrue"])
	set("kSecMatchLimit", api.constants["kSecMatchLimitAll"])
	var result uintptr
	status = api.itemCopyMatching(query, &result)
	if result != 0 {
		defer api.release(result)
	}
	response.Status = keychainNativeStatus(status)
	if status != 0 {
		return response
	}
	response.Status = "invalid_record"
	if result == 0 || api.getTypeID(result) != api.arrayTypeID() {
		return response
	}
	count := api.arrayCount(result)
	if count > 1 {
		response.Status = "ambiguous"
		return response
	}
	if count != 1 {
		return response
	}
	data := api.arrayValue(result, 0)
	if data == 0 || api.getTypeID(data) != api.dataTypeID() {
		return response
	}
	length := api.dataLength(data)
	if length <= 0 || length > maxCredentialBytes {
		return response
	}
	pointer := api.dataPointer(data)
	if pointer == nil {
		return response
	}
	response.Data = bytes.Clone(unsafe.Slice((*byte)(pointer), length))
	response.Status = "ok"
	return response
}
