package integrations

import "errors"

var (
	ErrNameRequired         = errors.New("integration name is required")
	ErrInvalidType          = errors.New("integration type must be 'http-proxy' or 'git-proxy'")
	ErrInvalidAttachMode    = errors.New("attach mode must be 'sandbox', 'tag', or 'auto:all'")
	ErrAttachSelectorRequired = errors.New("attach selector is required for sandbox and tag attach modes")
	ErrTargetRequired       = errors.New("target URL is required for http-proxy integration")
	ErrSecretRequired       = errors.New("secret value is required")
	ErrInvalidSecretType    = errors.New("secret_type must be 'bearer', 'header', or 'basic-auth'")
	ErrNotFound             = errors.New("integration not found")
	ErrDuplicateName        = errors.New("integration with this name already exists")
	ErrEncryptFailed        = errors.New("failed to encrypt secret")
	ErrDecryptFailed        = errors.New("failed to decrypt secret")
)
