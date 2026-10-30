package connection

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/godbus/dbus/v5"
)

const secretServiceName = "org.freedesktop.secrets"
const secretServiceInterface = "org.freedesktop.Secret.Service"
const secretCollectionInterface = "org.freedesktop.Secret.Collection"
const secretItemInterface = "org.freedesktop.Secret.Item"

var secretServicePath = dbus.ObjectPath("/org/freedesktop/secrets")

// Secret Service's plain session stays on an authenticated local Unix D-Bus
// connection. The OS service owns encryption at rest. No helper process, shell,
// automatic service start, collection creation, unlock, or prompt is used.
type secretServiceVault struct{ address, scope string }

type serviceSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

type serviceConnection struct {
	conn       *dbus.Conn
	owner      string
	collection dbus.ObjectPath
	session    dbus.ObjectPath
}

func openSecretServiceVault(ctx context.Context, root string) (CredentialVault, error) {
	if runtime.GOOS != "linux" {
		return nil, errCredentialStoreUnavailable
	}
	address := strings.TrimSpace(os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	if address == "" {
		return nil, errCredentialStoreUnavailable
	}
	// Never send credentials to a network D-Bus endpoint or an address fallback.
	if !strings.HasPrefix(address, "unix:") || strings.Contains(address, ";") {
		return nil, credentialFailure("credential_store_unavailable", "use an authenticated local Unix session bus, or explicitly select file credential storage")
	}
	scope, err := credentialConfiguration(root)
	if err != nil {
		return nil, credentialFailure("credential_store_unavailable", "restore the absolute AgentLab configuration directory")
	}
	vault := secretServiceVault{address: address, scope: scope}
	service, err := vault.open(ctx)
	if err != nil {
		return nil, err
	}
	service.conn.Close()
	return vault, nil
}

func (secretServiceVault) Name() string { return "secret-service" }

func connectCredentialBus(ctx context.Context, address string) (*dbus.Conn, error) {
	encoded, ok := strings.CutPrefix(address, "unix:")
	if !ok {
		return nil, fmt.Errorf("a local Unix bus is required")
	}
	path := ""
	for _, pair := range strings.Split(encoded, ",") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("invalid Unix bus address")
		}
		value, err := dbus.UnescapeBusAddressValue(value)
		if err != nil {
			return nil, fmt.Errorf("invalid Unix bus address")
		}
		switch key {
		case "path", "abstract":
			if path != "" || value == "" || strings.ContainsRune(value, 0) {
				return nil, fmt.Errorf("ambiguous Unix bus address")
			}
			path = value
			if key == "abstract" {
				path = "\x00" + value
			}
		case "guid":
		default:
			return nil, fmt.Errorf("unsupported Unix bus address")
		}
	}
	if path == "" {
		return nil, fmt.Errorf("bus address has no unix socket")
	}
	dialer := net.Dialer{}
	raw, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	conn, err := dbus.NewConn(raw, dbus.WithContext(ctx))
	if err != nil {
		raw.Close()
		return nil, err
	}
	if err := conn.Auth(nil); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (v secretServiceVault) open(ctx context.Context) (*serviceConnection, error) {
	conn, err := connectCredentialBus(ctx, v.address)
	if err != nil {
		return nil, credentialFailure("credential_store_unavailable", "restore the local session bus, or explicitly select file credential storage for a new connection")
	}
	failed := true
	defer func() {
		if failed {
			conn.Close()
		}
	}()
	// Resolve once to a unique owner. A service replacement cannot receive a
	// credential midway through this operation.
	var owner string
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", dbus.FlagNoAutoStart, secretServiceName).Store(&owner); err != nil {
		if busErr, ok := err.(dbus.Error); ok && busErr.Name == "org.freedesktop.DBus.Error.NameHasNoOwner" {
			return nil, errCredentialStoreUnavailable
		}
		return nil, credentialFailure("credential_store_unavailable", "restore the current operating system credential service before retrying")
	}
	service := &serviceConnection{conn: conn, owner: owner}
	if err := service.call(ctx, secretServicePath, secretServiceInterface+".ReadAlias", "default").Store(&service.collection); err != nil || service.collection == "/" || !service.collection.IsValid() {
		return nil, credentialFailure("credential_store_locked", "create and unlock the default credential collection through the operating system, then retry; unattended clients can explicitly select file storage")
	}
	if err := service.requireUnlocked(ctx, service.collection, secretCollectionInterface); err != nil {
		return nil, err
	}
	var output dbus.Variant
	if err := service.call(ctx, secretServicePath, secretServiceInterface+".OpenSession", "plain", dbus.MakeVariant("")).Store(&output, &service.session); err != nil || !service.session.IsValid() || service.session == "/" {
		return nil, credentialFailure("credential_store_unavailable", "restore a Secret Service provider that supports local plain sessions, or explicitly select file storage")
	}
	failed = false
	return service, nil
}

func (s *serviceConnection) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	return s.conn.Object(s.owner, path).CallWithContext(ctx, method, dbus.FlagNoAutoStart, args...)
}

func (s *serviceConnection) requireUnlocked(ctx context.Context, path dbus.ObjectPath, iface string) error {
	var locked dbus.Variant
	if err := s.call(ctx, path, "org.freedesktop.DBus.Properties.Get", iface, "Locked").Store(&locked); err != nil {
		return credentialFailure("credential_store_unavailable", "restore access to the operating system credential store before retrying")
	}
	value, ok := locked.Value().(bool)
	if !ok || value {
		return credentialFailure("credential_store_locked", "unlock the credential collection through the operating system, then retry; AgentLab does not open an unlock prompt")
	}
	return nil
}

func (v secretServiceVault) attributes(ref string) map[string]string {
	return map[string]string{"application": "agentlab", "configuration": v.scope, "reference": ref}
}

func (v secretServiceVault) find(ctx context.Context, service *serviceConnection, ref string) (dbus.ObjectPath, error) {
	if !validCredentialReference(ref) {
		return "", credentialFailure("invalid_credential_reference", "restore the saved credential reference before retrying")
	}
	var unlocked, locked []dbus.ObjectPath
	if err := service.call(ctx, secretServicePath, secretServiceInterface+".SearchItems", v.attributes(ref)).Store(&unlocked, &locked); err != nil {
		return "", credentialFailure("credential_store_unavailable", "restore credential lookup in the operating system before retrying")
	}
	if len(locked) != 0 {
		return "", credentialFailure("credential_store_locked", "unlock this context's credential through the operating system before retrying")
	}
	if len(unlocked) == 0 {
		return "", nil
	}
	if len(unlocked) != 1 || !unlocked[0].IsValid() || unlocked[0] == "/" {
		return "", credentialFailure("ambiguous_credential_reference", "preserve the duplicate operating system credentials and reconnect with a new reference")
	}
	if err := service.requireUnlocked(ctx, unlocked[0], secretItemInterface); err != nil {
		return "", err
	}
	return unlocked[0], nil
}

func (v secretServiceVault) Save(ctx context.Context, ref string, data []byte) error {
	if len(data) > maxCredentialBytes {
		return credentialFailure("credential_too_large", "use a connection credential record smaller than 128 KiB")
	}
	service, err := v.open(ctx)
	if err != nil {
		return err
	}
	defer service.conn.Close()
	path, err := v.find(ctx, service, ref)
	if err != nil {
		return err
	}
	if path != "" {
		return credentialFailure("credential_reference_exists", "preserve the existing credential and retry with a new connection reference")
	}
	properties := map[string]dbus.Variant{
		secretItemInterface + ".Label":      dbus.MakeVariant("AgentLab connection"),
		secretItemInterface + ".Attributes": dbus.MakeVariant(v.attributes(ref)),
	}
	secret := serviceSecret{Session: service.session, Parameters: []byte{}, Value: data, ContentType: "application/json"}
	var item, prompt dbus.ObjectPath
	if err := service.call(ctx, service.collection, secretCollectionInterface+".CreateItem", properties, secret, false).Store(&item, &prompt); err != nil {
		return credentialFailure("credential_store_unavailable", "restore write access to the operating system credential store; the existing connection is preserved")
	}
	if prompt != "/" || !item.IsValid() || item == "/" {
		service.dismiss(ctx, prompt)
		return credentialFailure("credential_store_locked", "authorize credential storage through the operating system and retry; AgentLab does not open a prompt")
	}
	// Verify the saved bytes before the caller can publish a context reference.
	actual, err := service.read(ctx, item)
	if err != nil || !bytes.Equal(actual, data) {
		// The durable intent lets the caller reconcile this write. Do not
		// delete a record whose returned contents no longer prove ownership.
		return credentialFailure("credential_store_verification_failed", "credential storage did not verify; preserve the current connection and repair the operating system store before retrying")
	}
	return nil
}

func (s *serviceConnection) read(ctx context.Context, item dbus.ObjectPath) ([]byte, error) {
	var secret serviceSecret
	if err := s.call(ctx, item, secretItemInterface+".GetSecret", s.session).Store(&secret); err != nil || secret.Session != s.session || len(secret.Parameters) != 0 || (secret.ContentType != "application/json" && secret.ContentType != "text/plain") || len(secret.Value) > maxCredentialBytes {
		return nil, credentialFailure("invalid_credential_record", "restore this context's credential in the operating system store, or reconnect")
	}
	return secret.Value, nil
}

func (v secretServiceVault) Load(ctx context.Context, ref string) ([]byte, error) {
	service, err := v.open(ctx)
	if err != nil {
		return nil, err
	}
	defer service.conn.Close()
	item, err := v.find(ctx, service, ref)
	if err != nil {
		return nil, err
	}
	if item == "" {
		return nil, credentialFailure("credential_not_found", "restore the credential in its recorded operating system store, or reconnect with a replacement credential")
	}
	return service.read(ctx, item)
}

func (s *serviceConnection) dismiss(ctx context.Context, prompt dbus.ObjectPath) {
	if prompt.IsValid() && prompt != "/" {
		s.call(ctx, prompt, "org.freedesktop.Secret.Prompt.Dismiss")
	}
}

func (v secretServiceVault) Delete(ctx context.Context, ref string) error {
	service, err := v.open(ctx)
	if err != nil {
		return err
	}
	defer service.conn.Close()
	item, err := v.find(ctx, service, ref)
	if err != nil || item == "" {
		return err
	}
	var prompt dbus.ObjectPath
	if err := service.call(ctx, item, secretItemInterface+".Delete").Store(&prompt); err != nil {
		return credentialFailure("credential_cleanup_required", "restore access to the operating system credential store and retry removing the unused reference")
	}
	if prompt != "/" {
		service.dismiss(ctx, prompt)
		return credentialFailure("credential_cleanup_required", "authorize credential removal through the operating system before retrying; AgentLab does not open a prompt")
	}
	return nil
}
