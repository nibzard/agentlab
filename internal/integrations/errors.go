package integrations

import "errors"

var (
	ErrNameRequired           = errors.New("integration name is required")
	ErrInvalidType            = errors.New("integration type must be 'http-proxy', 'git-proxy', or 'llm-proxy'")
	ErrInvalidAttachMode      = errors.New("attach mode must be 'sandbox', 'tag', or 'auto:all'")
	ErrAttachSelectorRequired = errors.New("attach selector is required for sandbox and tag attach modes")
	ErrTargetRequired         = errors.New("target URL is required for http-proxy integration")
	ErrSecretRequired         = errors.New("secret value is required")
	ErrInvalidSecretType      = errors.New("secret_type must be 'bearer', 'header', or 'basic-auth'")
	ErrInvalidProvider        = errors.New("llm provider must be 'openai', 'anthropic', or 'ollama'")
	ErrInvalidTargetScheme    = errors.New("integration target scheme must be http or https")
	ErrInvalidTargetHost      = errors.New("integration target host is not allowed")
	ErrNotFound               = errors.New("integration not found")
	ErrDuplicateName          = errors.New("integration with this name already exists")
	ErrEncryptFailed          = errors.New("failed to encrypt secret")
	ErrDecryptFailed          = errors.New("failed to decrypt secret")
)
