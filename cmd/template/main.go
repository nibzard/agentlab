package main

import (
	"fmt"
	"strings"
	"time"
)

const defaultImageURL = "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"

type TemplateCreateCommand struct{}

func (c *TemplateCreateCommand) Synopsis() string {
	return "Generate a prompt for creating an AgentLab template"
}

func (c *TemplateCreateCommand) Usage() string {
	return "agentlab template create <description>"
}

func (c *TemplateCreateCommand) Execute(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("description required")
	}

	description := strings.Join(args, " ")
	name := generateName(description)
	displayName := generateDisplayName(description)
	packages := generatePackages(description)
	tags := generateTags(description)
	imageURL := defaultImageURL

	fmt.Println("# AgentLab Template Creation Instructions")
	fmt.Println("")
	fmt.Println("You are helping create an AgentLab template.")
	fmt.Println("")
	fmt.Println("REQUIREMENTS:")
	fmt.Println("- Proxmox VE 8.x+ or 9.x+ host")
	fmt.Println("- Template will be used with agentlab CLI")
	fmt.Println("- Must enable QEMU guest agent (critical)")
	fmt.Println("- Network: vmbr0 (full access) or vmbr1 (isolated)")
	fmt.Println("")
	fmt.Println("SCHEMA REQUIREMENTS:")
	fmt.Println("- Create template YAML at: /etc/agentlab/templates/<name>.yml")
	fmt.Println("- Follow the schema structure defined below")
	fmt.Println("- All required fields must be present")
	fmt.Println("")
	fmt.Println("REQUIRED FIELDS:")
	fmt.Println("- name: unique identifier (alphanumeric, hyphens)")
	fmt.Println("- display_name: human-readable name")
	fmt.Println("- image.url: OS image source URL")
	fmt.Println("- proxmox.vmid: integer, >= 9000 (auto-assign 9xxx range)")
	fmt.Println("- proxmox.agent: 1 (CRITICAL - QEMU guest agent must be enabled)")
	fmt.Println("- cloud_init.install_qemu_guest_agent: true (CRITICAL)")
	fmt.Println("- agentlab.profile_name: profile identifier")
	fmt.Println("")
	fmt.Println("VALUE CONSTRAINTS:")
	fmt.Println("- proxmox.memory_mb: 2048-16384 (power of 2)")
	fmt.Println("- proxmox.cores: 1-8")
	fmt.Println("- proxmox.storage.disk_size_gb: 20-200")
	fmt.Println("")
	fmt.Println("TEMPLATE STRUCTURE:")
	fmt.Println("```yaml")
	fmt.Println("name: " + name)
	fmt.Println("display_name: \"" + displayName + "\"")
	fmt.Println("description: \"" + description + "\"")
	fmt.Println("version: \"1.0.0\"")
	fmt.Println("created_by: \"generated\"")
	fmt.Println("created_at: \"" + time.Now().Format(time.RFC3339) + "\"")
	fmt.Println("")
	fmt.Println("image:")
	fmt.Println("  url: " + imageURL)
	fmt.Println("  type: cloud-image")
	fmt.Println("")
	fmt.Println("proxmox:")
	fmt.Println("  vmid: 9007")
	fmt.Println("  name: \"" + name + "-template\"")
	fmt.Println("  memory_mb: 2048")
	fmt.Println("  cores: 1")
	fmt.Println("  storage:")
	fmt.Println("    pool: local-zfs")
	fmt.Println("    disk_size_gb: 35")
	fmt.Println("    disk_type: scsi")
	fmt.Println("  network:")
	fmt.Println("    bridge: vmbr0")
	fmt.Println("    model: virtio")
	fmt.Println("    agent: 1")
	fmt.Println("")
	fmt.Println("cloud_init:")
	fmt.Println("  user:")
	fmt.Println("    name: coder")
	fmt.Println("    sudo: ALL=(ALL) NOPASSWD:ALL")
	fmt.Println("    shell: /bin/bash")
	fmt.Println("    lock_passwd: true")
	fmt.Println("    ssh_keys_from_host: true")
	fmt.Println("  network:")
	fmt.Println("    dns_servers:")
	fmt.Println("      - 1.1.1.1")
	fmt.Println("      - 8.8.8.8")
	fmt.Println("    hostname: \"" + name + "-dev\"")
	fmt.Println("    timezone: UTC")
	fmt.Println("    ssh_pwauth: false")
	fmt.Println("  install_qemu_guest_agent: true")
	fmt.Println("  runcmd:")
	fmt.Println("    - apt-get update")
	fmt.Println("    - apt-get install -y " + strings.Join(packages, " "))
	fmt.Println("")
	fmt.Println("agentlab:")
	fmt.Println("  profile_name: " + name)
	fmt.Println("  network_bridge: vmbr0")
	fmt.Println("  behavior_mode: dangerous")
	fmt.Println("  ttl_minutes_default: 240")
	fmt.Println("  repo_clone_path: /home/coder/projects")
	fmt.Println("  tags: [" + strings.Join(tags, ", ") + "]")
	fmt.Println("  category: development")
	fmt.Println("")
	fmt.Println("```")
	fmt.Println("")
	fmt.Println("STEP-BY-STEP INSTRUCTIONS:")
	fmt.Println("")
	fmt.Println("1. Parse user's request to identify:")
	fmt.Println("   - Operating system preference (default: Ubuntu 24.04 LTS if not specified)")
	fmt.Println("   - Resource requirements (defaults: 2GB RAM, 1 core, 35GB disk if not specified)")
	fmt.Println("   - Programming languages needed")
	fmt.Println("   - Tools required")
	fmt.Println("")
	fmt.Println("2. Generate YAML structure:")
	fmt.Println("   - Use the template structure shown above")
	fmt.Println("   - Set name to: lowercase, alphanumeric with hyphens, generated from description")
	fmt.Println("   - Set display_name to: human-readable summary of purpose")
	fmt.Println("   - Set description to: clear explanation of what this template does")
	fmt.Println("   - Set created_by: \"generated\"")
	fmt.Println("   - Set created_at: current timestamp in ISO 8601 format")
	fmt.Println("   - Set image.url to Ubuntu 24.04 cloud image if OS not specified")
	fmt.Println("   - Set proxmox.vmid: auto-assign next available ID in 9xxx range")
	fmt.Println("   - Set proxmox.memory_mb, cores, disk_size_gb based on requirements")
	fmt.Println("   - CRITICAL: Set proxmox.agent: 1 (QEMU guest agent enabled)")
	fmt.Println("   - CRITICAL: Set cloud_init.install_qemu_guest_agent: true")
	fmt.Println("   - Set cloud_init.user.name: \"coder\"")
	fmt.Println("   - Set cloud_init.user.sudo: ALL=(ALL) NOPASSWD:ALL")
	fmt.Println("   - Set cloud_init.user.shell: /bin/bash")
	fmt.Println("   - Set cloud_init.user.lock_passwd: true")
	fmt.Println("   - Set cloud_init.user.ssh_keys_from_host: true")
	fmt.Println("   - Set cloud_init.network.dns_servers to: [1.1.1.1, 8.8.8.8]")
	fmt.Println("   - Set cloud_init.network.hostname to: \"" + name + "-dev\"")
	fmt.Println("   - Set cloud_init.network.timezone to: UTC")
	fmt.Println("   - Set cloud_init.network.ssh_pwauth: false")
	fmt.Println("   - Set cloud_init.runcmd: basic system update + detected packages")
	fmt.Println("   - Set agentlab.profile_name: match template name")
	fmt.Println("   - Set agentlab.network_bridge: vmbr0 for dev, vmbr1 for isolated")
	fmt.Println("   - Set agentlab.behavior_mode: \"dangerous\"")
	fmt.Println("   - Set agentlab.ttl_minutes_default: 240 (4 hours)")
	fmt.Println("   - Set agentlab.keepalive_default: true")
	fmt.Println("   - Set agentlab.repo_clone_path: /home/coder/projects")
	fmt.Println("")
	fmt.Println("3. Validate before output:")
	fmt.Println("   - Check all required fields are present: name, display_name, image, proxmox")
	fmt.Println("   - Verify CRITICAL fields: proxmox.agent: 1, cloud_init.install_qemu_guest_agent: true")
	fmt.Println("   - Verify agentlab.profile_name is set")
	fmt.Println("   - Verify resource values are within constraints")
	fmt.Println("   - Ensure memory_mb is power of 2 (2048, 4096, 8192, 16384)")
	fmt.Println("   - Ensure cores is 1-8")
	fmt.Println("   - Ensure disk_size_gb is 20-200")
	fmt.Println("")
	fmt.Println("4. Output complete YAML:")
	fmt.Println("   - Include all sections: name, display_name, version, created_by, created_at, image, proxmox, cloud_init, agentlab, packages, tags, category")
	fmt.Println("   - Use 2-space indentation for YAML")
	fmt.Println("   - Add comments for clarity")
	fmt.Println("   - Ensure YAML syntax is valid")
	fmt.Println("")
	fmt.Println("5. Deliverables:")
	fmt.Println("   - Write template YAML to: /etc/agentlab/templates/<name>.yml")
	fmt.Println("   - Create directory if it doesn't exist: /etc/agentlab/templates/")
	fmt.Println("   - Output: file path for user reference")
	fmt.Println("")
	fmt.Println("COMMON MISTAKES TO AVOID:")
	fmt.Println("")
	fmt.Println("1. FORGETTING QEMU GUEST AGENT")
	fmt.Println("   - CRITICAL: Templates without QEMU guest agent fail AgentLab validation")
	fmt.Println("   - ERROR: \"template VM <vmid> does not have qemu-guest-agent enabled\"")
	fmt.Println("   - FIX: Always include \"agent: 1\" in proxmox section AND")
	fmt.Println("     \"install_qemu_guest_agent: true\" in cloud_init section AND")
	fmt.Println("     \"apt-get install -y qemu-guest-agent\" in runcmd")
	fmt.Println("")
	fmt.Println("2. WRONG VMID FORMAT")
	fmt.Println("   - ERROR: Use VMIDs that conflict with existing templates")
	fmt.Println("   - FIX: Auto-assign from 9xxx range or let user specify")
	fmt.Println("   - EXAMPLE: 9007, 9008, 9009 are safe for user templates")
	fmt.Println("")
	fmt.Println("3. INVALID PACKAGE NAMES")
	fmt.Println("   - ERROR: Package names don't exist in Ubuntu repositories")
	fmt.Println("   - FIX: Use valid Ubuntu package names only")
	fmt.Println("   - EXAMPLE: \"ripgrep\" not \"ripgrep-cli\", \"fd-find\" not \"fd\"")
	fmt.Println("")
	fmt.Println("4. MEMORY NOT POWER OF 2")
	fmt.Println("   - ERROR: Odd numbers cause Proxmox issues")
	fmt.Println("   - FIX: Use 2048, 4096, 8192, 16384 (powers of 2)")
	fmt.Println("   - WRONG: 3000, 5000, 7000")
	fmt.Println("")
	fmt.Println("5. MISSING AGENTLAB PROFILE")
	fmt.Println("   - ERROR: Template created but no AgentLab profile")
	fmt.Println("   - FIX: Always include \"agentlab\" section with \"profile_name\"")
	fmt.Println("   - Users can't use \"agentlab sandbox new --profile <name>\" without it")
	fmt.Println("")
	fmt.Println("6. NETWORK BRIDGE MISMATCH")
	fmt.Println("   - ERROR: vmbr1 specified but user wants full LAN access")
	fmt.Println("   - FIX: Ask or infer from use case")
	fmt.Println("   - Interactive/dev: vmbr0, isolated/automated: vmbr1")
	fmt.Println("")
	fmt.Println("DELIVERABLES CHECKLIST:")
	fmt.Println("")
	fmt.Println("After generating the template YAML, ensure you have:")
	fmt.Println("- [ ] Template YAML file created at: /etc/agentlab/templates/<name>.yml")
	fmt.Println("- [ ] YAML syntax is valid (no parsing errors)")
	fmt.Println("- [ ] All required fields present: name, display_name, image, proxmox")
	fmt.Println("- [ ] CRITICAL: QEMU guest agent enabled (proxmox.agent: 1 AND cloud_init.install_qemu_guest_agent: true)")
	fmt.Println("- [ ] CRITICAL: AgentLab profile section present with profile_name")
	fmt.Println("- [ ] Resource allocations within valid ranges")
	fmt.Println("- [ ] Memory is power of 2")
	fmt.Println("- [ ] Cores between 1-8")
	fmt.Println("- [ ] Disk size between 20-200GB")
	fmt.Println("- [ ] Network bridge is appropriate for use case")
	fmt.Println("- [ ] Package names are valid Ubuntu packages")
	fmt.Println("- [ ] YAML uses 2-space indentation")
	fmt.Println("- [ ] Template can be validated with: agentlab template validate <name>")
	fmt.Println("")
	fmt.Println("NOTES:")
	fmt.Println("- Assumed Ubuntu 24.04 LTS (most common for dev templates)")
	fmt.Println("- Used default resource allocation: 2GB RAM, 1 core, 35GB disk")
	fmt.Println("- Network set to vmbr0 for full LAN access (development work)")
	fmt.Println("- Template can be customized by editing the generated YAML file")
	fmt.Println("- Users can verify template with: qm config <vmid>")
	fmt.Println("- Users can test sandbox creation: agentlab sandbox new --profile <name>")
	fmt.Println("")
	fmt.Println("Generate the complete YAML template following all instructions above.")

	return nil
}

func generateName(description string) string {
	words := strings.Fields(strings.ToLower(description))
	name := strings.Join(words[:min(3, len(words))], "-")
	return "ubuntu-" + name
}

func generateDisplayName(description string) string {
	words := strings.Fields(description)
	if len(words) > 4 {
		return strings.Join(words[:4], " ")
	}
	return strings.Join(words, " ")
}

func generateDescription(description string) string {
	return description
}

func generatePackages(description string) []string {
	packages := []string{
		"openssh-server",
		"git",
		"curl",
		"jq",
		"wget",
	}

	desc := strings.ToLower(description)
	if strings.Contains(desc, "python") {
		packages = append(packages, "python3", "python3-pip")
	}
	if strings.Contains(desc, "go") || strings.Contains(desc, "golang") {
		packages = append(packages, "build-essential")
	}
	if strings.Contains(desc, "git") {
		packages = append(packages, "gh", "ripgrep")
	}
	if strings.Contains(desc, "dev") || strings.Contains(desc, "development") {
		packages = append(packages, "fzf", "fd-find", "tmux", "zsh", "make")
	}

	return packages
}

func generateTags(description string) []string {
	tags := []string{"ubuntu", "development"}
	desc := strings.ToLower(description)

	if strings.Contains(desc, "python") {
		tags = append(tags, "python")
	}
	if strings.Contains(desc, "go") || strings.Contains(desc, "golang") {
		tags = append(tags, "go")
	}
	if strings.Contains(desc, "rust") {
		tags = append(tags, "rust")
	}
	if strings.Contains(desc, "minimal") {
		tags = append(tags, "lightweight")
	}
	if strings.Contains(desc, "full") || strings.Contains(desc, "complete") {
		tags = append(tags, "full-stack")
	}

	return tags
}
