package daemon

import (
	"net/http"
	"strings"
)

const daemonErrorCodeVersion = "v1"

const (
	// Auth domain
	daemonErrorCodeAuthMissingBearerToken = daemonErrorCodeVersion + "/auth/missing_bearer_token"
	daemonErrorCodeAuthInvalidBearerToken = daemonErrorCodeVersion + "/auth/invalid_bearer_token"
	daemonErrorCodeAuthRemoteAddress     = daemonErrorCodeVersion + "/auth/remote_address_denied"
	daemonErrorCodeAuthUnauthorized      = daemonErrorCodeVersion + "/auth/unauthorized"
	daemonErrorCodeAuthForbidden         = daemonErrorCodeVersion + "/auth/forbidden"

	// Validation domain
	daemonErrorCodeValidationBadRequest      = daemonErrorCodeVersion + "/validation/bad_request"
	daemonErrorCodeValidationMalformedJSON    = daemonErrorCodeVersion + "/validation/malformed_json"
	daemonErrorCodeValidationMissingField    = daemonErrorCodeVersion + "/validation/missing_required_field"
	daemonErrorCodeValidationInvalidValue    = daemonErrorCodeVersion + "/validation/invalid_value"
	daemonErrorCodeValidationConflict        = daemonErrorCodeVersion + "/validation/conflict"
	daemonErrorCodeValidationUnknownProfile  = daemonErrorCodeVersion + "/validation/unknown_profile"

	// Provisioning domain
	daemonErrorCodeProvisioningFailed       = daemonErrorCodeVersion + "/provisioning/failed"
	daemonErrorCodeProvisioningLeaseHeld    = daemonErrorCodeVersion + "/provisioning/lease_held"
	daemonErrorCodeProvisioningUnavailable  = daemonErrorCodeVersion + "/provisioning/unavailable"
	daemonErrorCodeProvisioningStateConflict = daemonErrorCodeVersion + "/provisioning/state_conflict"

	// Workspace domain
	daemonErrorCodeWorkspaceNotFound      = daemonErrorCodeVersion + "/workspace/not_found"
	daemonErrorCodeWorkspaceAlreadyExists = daemonErrorCodeVersion + "/workspace/already_exists"
	daemonErrorCodeWorkspaceDetached      = daemonErrorCodeVersion + "/workspace/detached_required"
	daemonErrorCodeWorkspaceUnavailable   = daemonErrorCodeVersion + "/workspace/unavailable"
	daemonErrorCodeWorkspaceInvalidState  = daemonErrorCodeVersion + "/workspace/invalid_state"

	// Network domain
	daemonErrorCodeNetworkInvalidTarget = daemonErrorCodeVersion + "/network/invalid_target"
	daemonErrorCodeNetworkInvalidPort   = daemonErrorCodeVersion + "/network/invalid_port"
	daemonErrorCodeNetworkInvalidIP     = daemonErrorCodeVersion + "/network/invalid_ip"

	// Artifacts domain
	daemonErrorCodeArtifactsNotFound      = daemonErrorCodeVersion + "/artifacts/not_found"
	daemonErrorCodeArtifactsPathInvalid   = daemonErrorCodeVersion + "/artifacts/invalid_path"
	daemonErrorCodeArtifactsUnavailable  = daemonErrorCodeVersion + "/artifacts/unavailable"

	// Generic fallbacks
	daemonErrorCodeResourceNotFound  = daemonErrorCodeVersion + "/resource/not_found"
	daemonErrorCodeConflict         = daemonErrorCodeVersion + "/resource/conflict"
	daemonErrorCodeInternalError    = daemonErrorCodeVersion + "/internal/error"
	daemonErrorCodeServerError      = daemonErrorCodeVersion + "/internal/server_error"
	daemonErrorCodeUnavailable      = daemonErrorCodeVersion + "/internal/unavailable"
)

func daemonErrorCode(status int, message string) string {
	normalized := strings.TrimSpace(strings.ToLower(message))
	if normalized != "" {
		if code := daemonErrorCodeFromMessage(status, normalized); code != "" {
			return code
		}
	}
	return daemonErrorCodeByStatus(status)
}

func daemonErrorCodeFromMessage(status int, normalized string) string {
	switch {
	case strings.Contains(normalized, "missing bearer token"):
		return daemonErrorCodeAuthMissingBearerToken
	case strings.Contains(normalized, "invalid bearer token"):
		return daemonErrorCodeAuthInvalidBearerToken
	case strings.Contains(normalized, "remote address not allowed"):
		return daemonErrorCodeAuthRemoteAddress
	case strings.Contains(normalized, "request body is required"):
		return daemonErrorCodeValidationMissingField
	case strings.Contains(normalized, "invalid request body"):
		return daemonErrorCodeValidationMalformedJSON
	case strings.Contains(normalized, "unexpected trailing data"):
		return daemonErrorCodeValidationMalformedJSON
	case strings.Contains(normalized, "invalid profile defaults"):
		return daemonErrorCodeValidationInvalidValue
	case strings.Contains(normalized, "unknown profile"):
		return daemonErrorCodeValidationUnknownProfile
	case strings.Contains(normalized, "workspace lease held"):
		return daemonErrorCodeProvisioningLeaseHeld
	case strings.Contains(normalized, "workspace not found"):
		return daemonErrorCodeWorkspaceNotFound
	case strings.Contains(normalized, "workspace already exists"):
		return daemonErrorCodeWorkspaceAlreadyExists
	case strings.Contains(normalized, "workspace already attached"):
		return daemonErrorCodeWorkspaceInvalidState
	case strings.Contains(normalized, "workspace must be detached"):
		return daemonErrorCodeWorkspaceDetached
	case strings.Contains(normalized, "workspace manager unavailable"):
		return daemonErrorCodeWorkspaceUnavailable
	case strings.Contains(normalized, "failed to provision sandbox"):
		return daemonErrorCodeProvisioningFailed
	case strings.Contains(normalized, "session must") && strings.Contains(normalized, "attached workspace"):
		return daemonErrorCodeWorkspaceInvalidState
	case strings.Contains(normalized, "artifact root not configured"):
		return daemonErrorCodeArtifactsUnavailable
	case strings.Contains(normalized, "artifact root is not configured"):
		return daemonErrorCodeArtifactsUnavailable
	case strings.Contains(normalized, "artifact path is invalid") || (strings.Contains(normalized, "artifact") && strings.Contains(normalized, "invalid path")):
		return daemonErrorCodeArtifactsPathInvalid
	case strings.Contains(normalized, "artifact") && strings.Contains(normalized, "not found"):
		return daemonErrorCodeArtifactsNotFound
	case strings.Contains(normalized, "artifact"):
		if strings.Contains(normalized, "missing") && strings.Contains(normalized, "artifact") {
			return daemonErrorCodeArtifactsNotFound
		}
	case strings.Contains(normalized, "target_ip"):
		return daemonErrorCodeNetworkInvalidTarget
	case strings.Contains(normalized, "port must be between"):
		return daemonErrorCodeNetworkInvalidPort
	case strings.Contains(normalized, "invalid ip"):
		return daemonErrorCodeNetworkInvalidIP
	case strings.Contains(normalized, "invalid socket"):
		return daemonErrorCodeNetworkInvalidIP
	case strings.Contains(normalized, "not found"):
		switch {
		case strings.Contains(normalized, "session"):
			return daemonErrorCodeResourceNotFound
		case strings.Contains(normalized, "job"):
			return daemonErrorCodeResourceNotFound
		case strings.Contains(normalized, "workspace"):
			return daemonErrorCodeWorkspaceNotFound
		case strings.Contains(normalized, "sandbox"):
			return daemonErrorCodeResourceNotFound
		case strings.Contains(normalized, "exposure"):
			return daemonErrorCodeResourceNotFound
		case strings.Contains(normalized, "artifact"):
			return daemonErrorCodeArtifactsNotFound
		default:
			return daemonErrorCodeResourceNotFound
		}
	case strings.Contains(normalized, "already exists"):
		switch {
		case strings.Contains(normalized, "workspace"):
			return daemonErrorCodeWorkspaceAlreadyExists
		default:
			return daemonErrorCodeConflict
		}
	case strings.Contains(normalized, "invalid value"):
		return daemonErrorCodeValidationInvalidValue
	case strings.Contains(normalized, "is required") || strings.Contains(normalized, "must be set") || strings.Contains(normalized, "must specify"):
		return daemonErrorCodeValidationMissingField
	case strings.Contains(normalized, "invalid vmid"):
		return daemonErrorCodeValidationInvalidValue
	case strings.Contains(normalized, "invalid state"):
		return daemonErrorCodeProvisioningStateConflict
	case strings.Contains(normalized, "invalid json"):
		return daemonErrorCodeValidationMalformedJSON
	case strings.Contains(normalized, "invalid request"):
		return daemonErrorCodeValidationBadRequest
	case strings.Contains(normalized, "conflict"):
		return daemonErrorCodeValidationConflict
	case strings.Contains(normalized, "unavailable"):
		if status >= http.StatusInternalServerError {
			return daemonErrorCodeUnavailable
		}
		return daemonErrorCodeValidationConflict
	}
	return ""
}

func daemonErrorCodeByStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return daemonErrorCodeAuthUnauthorized
	case http.StatusForbidden:
		return daemonErrorCodeAuthForbidden
	case http.StatusBadRequest:
		return daemonErrorCodeValidationBadRequest
	case http.StatusNotFound:
		return daemonErrorCodeResourceNotFound
	case http.StatusConflict:
		return daemonErrorCodeConflict
	case http.StatusInternalServerError:
		return daemonErrorCodeServerError
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusBadGateway:
		return daemonErrorCodeUnavailable
	default:
		if status >= http.StatusInternalServerError {
			return daemonErrorCodeServerError
		}
	}
	return daemonErrorCodeInternalError
}
