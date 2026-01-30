package config

import (
	"strings"
	"testing"
)

func TestValidateWildcardRequiresAgentSubnet(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BootstrapListen = "0.0.0.0:8844"
	cfg.ArtifactListen = "0.0.0.0:8846"
	cfg.ControllerURL = "http://10.77.0.1:8844"
	cfg.ArtifactUploadURL = "http://10.77.0.1:8846/upload"
	cfg.AgentSubnet = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "agent_subnet") {
		t.Fatalf("expected agent_subnet error, got %v", err)
	}
}

func TestValidateWildcardRequiresControllerURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BootstrapListen = "0.0.0.0:8844"
	cfg.ControllerURL = ""
	cfg.AgentSubnet = "10.77.0.0/16"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "controller_url") {
		t.Fatalf("expected controller_url error, got %v", err)
	}
}

func TestValidateWildcardRequiresArtifactUploadURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ArtifactListen = "0.0.0.0:8846"
	cfg.ArtifactUploadURL = ""
	cfg.AgentSubnet = "10.77.0.0/16"
	cfg.ControllerURL = "http://10.77.0.1:8844"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "artifact_upload_url") {
		t.Fatalf("expected artifact_upload_url error, got %v", err)
	}
}

func TestValidateWildcardAcceptsExplicitConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BootstrapListen = "0.0.0.0:8844"
	cfg.ArtifactListen = "0.0.0.0:8846"
	cfg.AgentSubnet = "10.77.0.0/16"
	cfg.ControllerURL = "http://10.77.0.1:8844"
	cfg.ArtifactUploadURL = "http://10.77.0.1:8846/upload"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
