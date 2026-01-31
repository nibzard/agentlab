# AgentLab Development-Ready Template Guide

Create a VM template that includes popular coding agents and development environments.

---

## Table of Contents

1. [Overview](#1-overview)
2. [What's Included](#2-whats-included)
3. [Creating the Template](#3-creating-the-template)
4. [Template Specifications](#4-template-specifications)
5. [Included Tools](#5-included-tools)
6. [Post-Install Verification](#6-post-install-verification)
7. [Updating the Template](#7-updating-the-template)
8. [Usage Examples](#8-usage-examples)

---

## 1. Overview

This guide creates an **AgentLab Development-Ready Template** that comes pre-configured with:

✅ Popular AI coding agents (Claude Code CLI, GitHub Copilot CLI, etc.)
✅ Complete development environments for multiple languages
✅ Essential development tools and utilities
✅ Optimized for coding workflows

**Benefits:**
- Zero-setup coding - just clone and start
- Consistent environments across sessions
- No need to install tools every time
- Perfect for pair programming, code reviews, quick fixes

---

## 2. What's Included

### 2.1 AI Coding Agents

| Tool | Version | Install Method |
|------|---------|---------------|
| Claude Code CLI | Latest | npm |
| GitHub Copilot CLI | Latest | npm |
| Tabby | Latest | npm |
| Continue.dev CLI | Latest | npm |
| Codeium CLI | Latest | npm |

### 2.2 Development Environments

| Language | Core | Package Managers | Linters/Formatters | Testing |
|----------|------|-----------------|-------------------|----------|
| Python | 3.11 | pip, poetry, uv | black, flake8, mypy, ruff | pytest, unittest |
| Node.js | 20 LTS | npm, yarn, pnpm | eslint, prettier, @types/ | jest, vitest |
| Go | 1.21 | go modules | gofmt, gopls | go test, go vet |
| Java | 21 (Temurin) | maven, gradle | spotbugs, checkstyle | junit, testng |
| Rust | Stable | cargo | rustfmt, clippy | cargo test |
| Ruby | 3.2 | bundler, gem | rubocop, standard | rspec, minitest |
| PHP | 8.2 | composer | phpcs, php-cs-fixer | phpunit, pest |

### 2.3 Essential Tools

- **Version Control:** Git, gh (GitHub CLI)
- **Editors/IDE Helpers:** jq, ripgrep, fd, bat, exa, fzf
- **Terminal:** tmux, zsh with oh-my-zsh
- **Network Tools:** curl, wget, tcpdump, nmap
- **Docker:** Docker, Docker Compose
- **Process Monitoring:** htop, iotop, net-tools
- **File Operations:** rsync, tar, zip, unzip, 7zip
- **Cloud Tools:** awscli, gcloud, azure-cli

---

## 3. Creating the Template

### 3.1 Base VM Setup

1. **Download Ubuntu Server ISO**
   ```bash
   wget https://releases.ubuntu.com/24.04/ubuntu-24.04-live-server-amd64.iso
   ```

2. **Create VM in Proxmox**
   ```bash
   qm create 9001 \
     --name "dev-template-base" \
     --memory 8192 \
     --cores 4 \
     --net0 virtio,bridge=vmbr0 \
     --scsihw virtio-scsi-pci \
     --scsi0 local-lvm:20,format=qcow2 \
     --ostype l26 \
     --ide0 local-lvm:cloudinit,media=cdrom
   ```

3. **Install Ubuntu**
   - Boot from ISO
   - Install Ubuntu Server (minimal)
   - Create user: `ubuntu` with password
   - Install OpenSSH Server
   - Enable password authentication (for convenience)

### 3.2 Cloud-Init Configuration

Create `/var/lib/vz/snippets/agentlab-dev-template.yaml`:

```yaml
#cloud-config
package_update: true
package_upgrade: true

# Set timezone
timezone: UTC

# Configure system
runcmd:
  # Set hostname
  - [sh, -c, 'echo "agentlab-dev-template" > /etc/hostname']
  - [sh, -c, 'hostname -F /etc/hostname']

  # Configure DNS
  - [sh, -c, 'echo "nameserver 8.8.8.8" > /etc/resolv.conf']
  - [sh, -c, 'echo "nameserver 8.8.4.4" >> /etc/resolv.conf']

  # Disable swap (optional, for performance)
  - [sh, -c, 'swapoff -a || true']

  # Configure sysctl for development
  - [sh, -c, 'echo "fs.inotify.max_user_watches=524288" >> /etc/sysctl.conf']

# Create development user
users:
  - name: dev
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/zsh
    ssh_authorized_keys:
      - ssh-rsa AAAA... # Add your public key here
    lock_passwd: false

# Configure network
network:
  version: 2
  ethernets:
    eth0:
      dhcp4: true
      dhcp4-overrides:
        use-routes: true

# Packages to install
packages:
  # System tools
  - curl
  - wget
  - git
  - vim
  - nano
  - tmux
  - zsh
  - htop
  - iotop
  - net-tools
  - iputils-ping
  - dnsutils
  - tcpdump
  - rsync
  - tar
  - zip
  - unzip
  - p7zip-full
  - jq
  - ripgrep
  - fd-find
  - bat
  - exa
  - fzf

  # Build tools
  - build-essential
  - cmake
  - autoconf
  - automake
  - libtool

  # Version control
  - git
  - gh

  # Docker
  - docker.io
  - docker-compose

  # Cloud tools
  - awscli
  - google-cloud-cli
  - azure-cli

  # Python development
  - python3
  - python3-pip
  - python3-venv
  - python3-dev
  - python3.11-venv

  # Node.js development
  - nodejs
  - npm
  - yarnpkg

  # Go development
  - golang

  # Java development
  - default-jdk
  - maven
  - gradle

  # Rust development
  - rustc
  - cargo

  # Ruby development
  - ruby
  - ruby-dev
  - bundler

  # PHP development
  - php
  - php-cli
  - composer

# Run final setup script
runcmd:
  # Configure git
  - [sh, -c, 'git config --system user.name "AgentLab Developer"']
  - [sh, -c, 'git config --system user.email "dev@agentlab.local"']
  - [sh, -c, 'git config --system init.defaultBranch main']
  - [sh, -c, 'git config --system core.editor vim']

  # Configure Docker
  - [sh, -c, 'usermod -aG docker ubuntu']
  - [sh, -c, 'usermod -aG docker dev']

  # Configure zsh for dev user
  - [sh, -c, 'mkdir -p /home/dev/.config/zsh /home/dev/.cache']
  - [sh, -c, 'chown -R dev:dev /home/dev']

  # Clone and run post-install script
  - [sh, -c, 'curl -fsSL https://raw.githubusercontent.com/your-repo/agentlab-dev-template/main/scripts/post-install.sh | bash']
```

### 3.3 Post-Install Script

Create `scripts/post-install.sh` (hosted on GitHub):

```bash
#!/bin/bash
set -e

echo "=== AgentLab Dev Template Post-Install ==="

# Switch to dev user
su - dev <<'EOF'

# Install Oh My Zsh
echo "Installing Oh My Zsh..."
sh -c "$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)" "" --unattended

# Configure Zsh
echo "Configuring Zsh..."
cat >> ~/.zshrc << 'ZSHRC'

# Aliases for common tasks
alias ll='ls -lah'
alias la='ls -A'
alias l='ls -CF'
alias ..='cd ..'
alias ...='cd ../..'
alias g='git'
alias gs='git status'
alias ga='git add'
alias gc='git commit'
alias gp='git push'
alias gl='git pull'

# Language aliases
alias py='python3'
alias py3='python3'
alias pip='python3 -m pip'
alias node='node'
alias npm='npm'

# Docker aliases
alias d='docker'
alias dc='docker-compose'
alias dps='docker ps --format "table {{.ID}}\t{{.Image}}\t{{.Status}}"'
alias dlogs='docker logs -f --tail 100'
ZSHRC

# Install Python tools
echo "Installing Python tools..."
pip install --upgrade pip
pip install --user \
  pytest \
  black \
  flake8 \
  mypy \
  ruff \
  pylint \
  poetry \
  uv \
  jupyter \
  ipython \
  requests \
  numpy \
  pandas

# Install Node.js tools
echo "Installing Node.js tools..."
npm install -g \
  @anthropic-ai/claude-code \
  @githubnext/github-copilot-cli \
  tabby-cli \
  continue \
  codeium \
  typescript \
  tsx \
  eslint \
  prettier \
  @vue/cli \
  @react-native-community/cli

# Install Go tools
echo "Installing Go tools..."
go install \
  github.com/golangci/golangci-lint/cmd/golangci-lint@latest \
  github.com/golang/tools/gopls@latest \
  mvdan.cc/xurls/xurls@latest

# Install Rust tools
echo "Installing Rust tools..."
cargo install \
  cargo-watch \
  cargo-edit \
  bacon \
  cargo-binstall

# Install Java tools
echo "Installing Java tools..."
# Maven is already installed
# Gradle is already installed

# Install Ruby tools
echo "Installing Ruby tools..."
gem install \
  rubocop \
  rspec \
  pry \
  bundler-audit

# Install PHP tools
echo "Installing PHP tools..."
composer global require \
  phpstan/phpstan \
  friendsofphp/php-cs-fixer \
  phpunit/phpunit

# Install VS Code Server (for browser-based editing)
echo "Installing VS Code Server..."
wget -qO- https://github.com/coder/code-server/releases/download/v4.19.1/code-server-4.19.1-amd64.deb
dpkg -i code-server-4.19.1-amd64.deb
rm code-server-4.19.1-amd64.deb

# Configure Code Server
mkdir -p ~/.config/code-server
cat > ~/.config/code-server/config.yaml << 'CODESERVER'
bind-addr: 0.0.0.0:8080
auth: password
password: agentlab
cert: false
CODESERVER

# Create project directory structure
echo "Creating project directories..."
mkdir -p ~/projects/{python,nodejs,go,java,rust,php,ruby,general}
mkdir -p ~/temp
mkdir -p ~/downloads
mkdir -p ~/.local/{bin,share}

# Create startup script
cat > ~/start-coding-session.sh << 'STARTSCRIPT'
#!/bin/bash

# Source aliases
source ~/.zshrc

# Check for running tmux session
if tmux has-session -t dev 2>/dev/null; then
  echo "Session 'dev' already exists. Attach with: tmux attach -t dev"
  exit 1
fi

# Start new tmux session
tmux new-session -d -s dev -n "code" -c "cd ~/projects && zsh"

# Create windows
tmux send-keys -t dev 'split-window -h -p 25' C-m
tmux send-keys -t dev 'split-window -v -p 40' C-m

# Window 1: Editor/Code
tmux send-keys -t dev:0 "cd ~/projects/general" C-m
tmux send-keys -t dev:0 "clear" C-m
tmux send-keys -t dev:0 "echo 'Welcome to AgentLab Dev Environment!'" C-m
tmux send-keys -t dev:0 "echo ''" C-m
tmux send-keys -t dev:0 "echo 'Languages available: python, nodejs, go, java, rust, php, ruby'" C-m
tmux send-keys -t dev:0 "echo ''" C-m
tmux send-keys -t dev:0 "echo 'Claude Code CLI: claude'" C-m
tmux send-keys -t dev:0 "echo 'GitHub Copilot: gh copilot'" C-m

# Window 2: Terminal/Run
tmux send-keys -t dev:1 "cd ~/projects/general" C-m
tmux send-keys -t dev:1 "clear" C-m

# Attach to session
tmux attach -t dev
STARTSCRIPT

chmod +x ~/start-coding-session.sh

# Add to PATH
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc

# Create welcome message
cat > ~/.motd << 'MOTD'
╔══════════════════════════════════════════════╗
║                                                    ║
║   Welcome to AgentLab Development Template            ║
║                                                    ║
║   Start coding session: ./start-coding-session.sh   ║
║                                                    ║
║   Available Tools:                                 ║
║   - Claude Code CLI (claude)                     ║
║   - GitHub Copilot (gh copilot)                   ║
║   - Tabby CLI (tabby)                             ║
║   - Continue CLI (continue)                          ║
║                                                    ║
║   Languages:                                       ║
║   Python, Node.js, Go, Java, Rust, Ruby, PHP       ║
║                                                    ║
║   Quick Commands:                                   ║
║   ll, la, l - list directories                   ║
║   g - git alias                                   ║
║   d, dc - docker commands                          ║
║   py - python3 alias                              ║
║                                                    ║
║   VS Code Server: http://<IP>:8080           ║
║   Password: agentlab                                 ║
║                                                    ║
╚════════════════════════════════════════════════╝
MOTD

echo "Post-install complete!"
echo "Run 'tmux attach' or login as dev user to start coding"

EOF
```

### 3.4 Create Template VM

After base VM is installed and configured:

```bash
# Convert VM to template
qm template 9001 "dev-template" --description "AgentLab Development Template with AI coding agents"

# Or with a specific storage
qm template 9001 "dev-template" \
  --storage local-lvm \
  --full \
  --description "AgentLab Development Template with AI coding agents and dev environments"
```

---

## 4. Template Specifications

### 4.1 Resource Allocation

| Resource | Minimum | Recommended | Notes |
|----------|---------|--------------|-------|
| CPU Cores | 4 | 6-8 | More cores = faster compilation |
| Memory | 4GB | 6-8GB | Depends on language and tools |
| Storage | 40GB | 60-80GB | Enough for projects + dependencies |
| Network | vmbr1 | vmbr1 | Agent subnet for isolation |

### 4.2 VM Configuration

```yaml
name: dev-template
ostype: l26
cpu:
  cores: 4
  type: host
memory: 8192
network:
  device: virtio
  bridge: vmbr0
  firewall: false
scsi:
  hardware: virtio-scsi-pci
disk:
  type: scsi
  storage: local-lvm
  size: 40G
  format: qcow2
  cache: writeback
```

### 4.3 Proxmox Profile

Create `/etc/agentlab/profiles/dev-template.yaml`:

```yaml
name: dev-template
template_vmid: 9000  # Update to your template VMID
network:
  bridge: vmbr1
  model: virtio
  firewall_group: agent_nat_default
resources:
  cores: 4
  memory_mb: 8192
  balloon: false
storage:
  root_size_gb: 60
  workspace: attach
behavior:
  mode: dangerous
  keepalive_default: true
  ttl_minutes_default: 720  # 12 hours
secrets_bundle: default
repo:
  clone_path: /home/dev/projects
  artifacts:
    upload: true
    endpoint: http://10.77.0.1:8846/upload
```

---

## 5. Included Tools

### 5.1 AI Coding Agents

#### Claude Code CLI
```bash
# Already installed globally
claude --version

# Start interactive session
claude

# Generate code
claude "Create a REST API with user authentication"

# Explain code
claude explain src/auth.py

# Debug
claude debug "Getting 500 error on login"
```

#### GitHub Copilot CLI
```bash
# Authenticate
gh auth login --web

# Use in code
gh copilot suggest

# Inline suggestions in terminal
gh copilot suggest -t bash "Create a directory and list files"
```

#### Tabby
```bash
# Install (included)
tabby --version

# Generate code
tabby "Implement a sorting algorithm in Go"

# Interactive mode
tabby
```

#### Continue.dev CLI
```bash
# Install (included)
continue --version

# Generate code
continue "Create a React component for user profile"

# Inline suggestions
continue inline
```

### 5.2 Python Environment

```bash
# Activate virtual environment
cd ~/projects/python
python3 -m venv .venv
source .venv/bin/activate

# Install project dependencies
pip install -r requirements.txt

# Run tests
pytest tests/

# Format code
black src/
flake8 src/
mypy src/

# Jupyter notebook
jupyter notebook

# Poetry (alternative to pip)
poetry init
poetry add pandas numpy
poetry install
```

### 5.3 Node.js Environment

```bash
# Start new project
cd ~/projects/nodejs
npm create vite@latest my-app
cd my-app

# TypeScript
npm create vite@latest my-app -- --template react-ts

# Yarn alternative
yarn create vite my-app
yarn add react

# Pnpm alternative
pnpm create vite my-app
pnpm install

# Run tests
npm test
# or
jest

# ESLint
npm run lint
eslint src/

# Prettier
npx prettier --write src/

# Build
npm run build
```

### 5.4 Go Environment

```bash
# Initialize module
cd ~/projects/go
go mod init github.com/username/project

# Add dependencies
go get github.com/gin-gonic/gin
go get github.com/mattn/go-sqlite3

# Run tests
go test ./...

# Build
go build -o app ./cmd/server

# Format
go fmt ./...

# Lint
golangci-lint run

# Install tools
go install github.com/cweill/gotests/gotests@latest
gotests ./...
```

### 5.5 Java Environment

```bash
# Maven project
cd ~/projects/java
mvn archetype:generate -DgroupId=com.example -DartifactId=my-app

# Gradle project
gradle init --type java-application

# Run tests
mvn test
# or
gradle test

# Build
mvn package
# or
gradle build

# Run
java -jar target/my-app.jar
```

### 5.6 Rust Environment

```bash
# Initialize project
cd ~/projects/rust
cargo new my-app

# Add dependencies
cd my-app
cargo add serde tokio

# Run tests
cargo test

# Build
cargo build --release

# Run
cargo run

# Watch mode
cargo install cargo-watch
cargo watch -x run

# Format
cargo fmt

# Lint
cargo clippy
```

### 5.7 Additional Tools

#### Docker
```bash
# Run container
docker run -it --rm ubuntu:latest bash

# Docker Compose
docker-compose up -d

# View logs
docker-compose logs -f

# Build image
docker build -t myapp .
```

#### VS Code Server
```bash
# Access in browser
http://<sandbox-ip>:8080

# Login with password: agentlab

# Features available:
# - File browser
# - Terminal
# - Extensions (some)
# - Git integration
```

#### tmux Sessions
```bash
# Create new session
tmux new-session -s myproject

# List sessions
tmux ls

# Attach to session
tmux attach -t myproject

# Detach from session
# Ctrl+b, d

# Split windows
# Ctrl+b, "  # Horizontal split
# Ctrl+b, %  # Vertical split

# Switch panes
# Ctrl+b, Arrow keys
```

---

## 6. Post-Install Verification

### 6.1 Quick Verification Script

```bash
#!/bin/bash
# Run as dev user

echo "=== Verifying Installation ==="

# Check AI agents
echo -n "Claude Code CLI: "
claude --version && echo "✅ OK" || echo "❌ FAILED"

echo -n "GitHub Copilot: "
gh copilot --version && echo "✅ OK" || echo "⚠️  Needs auth"

echo -n "Tabby: "
tabby --version && echo "✅ OK" || echo "❌ FAILED"

# Check languages
echo -n "Python: "
python3 --version && echo "✅ OK" || echo "❌ FAILED"

echo -n "Node.js: "
node --version && echo "✅ OK" || echo "❌ FAILED"

echo -n "Go: "
go version && echo "✅ OK" || echo "❌ FAILED"

echo -n "Java: "
java -version && echo "✅ OK" || echo "❌ FAILED"

echo -n "Rust: "
rustc --version && echo "✅ OK" || echo "❌ FAILED"

# Check tools
echo -n "Git: "
git --version && echo "✅ OK" || echo "❌ FAILED"

echo -n "Docker: "
docker --version && echo "✅ OK" || echo "❌ FAILED"

echo -n "Docker Compose: "
docker-compose --version && echo "✅ OK" || echo "❌ FAILED"

echo -n "VS Code Server: "
code-server --version && echo "✅ OK" || echo "❌ FAILED"

echo ""
echo "=== Verification Complete ==="
```

### 6.2 Test Each Environment

```bash
# Test Python
python3 -c "import pandas, numpy, pytest; print('Python OK')"

# Test Node.js
node -e "console.log('Node.js OK')"

# Test Go
go version

# Test Java
javac -version

# Test Rust
cargo --version
```

---

## 7. Updating the Template

### 7.1 Updating Tools

When tools need updates, run on the template VM:

```bash
# Update all npm packages
npm update -g

# Update Python packages
pip install --upgrade pip
pip install --upgrade --user pytest black flake8 mypy

# Update Go tools
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Update Rust toolchain
rustup update
```

### 7.2 Recreating Template

After making changes to the template VM:

```bash
# 1. Shutdown the template VM
qm shutdown 9000

# 2. Remove old template
qm template 9000 "dev-template-old"

# 3. Delete old template
qm destroy 9000 --destroy-unreferenced-disks

# 4. Create new template from updated VM
qm template 9001 "dev-template" --description "Updated AgentLab Dev Template"
```

### 7.3 Versioning

Keep track of template versions:

```bash
# Tag templates
qm template 9000 "dev-template-v1"
qm template 9000 "dev-template-v2"

# Add notes
qm template 9000 "dev-template-v2.0" \
  --description "Added Rust toolchain and updated Node.js to v20"

# Keep changelog
cat > /etc/agentlab/template-changelog.md << 'CHANGELOG'
# AgentLab Dev Template Changelog

## v2.0.0 - 2026-01-31
### Added
- Rust toolchain (cargo, rustc, clippy)
- Updated Node.js to v20 LTS
- Added VS Code Server
- Improved tmux configuration

### Changed
- Updated Python to 3.11
- Improved post-install script performance
CHANGELOG
```

---

## 8. Usage Examples

### 8.1 Quick Coding Session (2 Hours)

```bash
# Create sandbox using dev-template
agentlab sandbox new \
  --profile dev-template \
  --name "quick-coding" \
  --ttl 2h

# Wait for RUNNING state, then SSH in
agentlab ssh <vmid>

# Or auto-attach with tmux
ssh -t dev@<IP> "tmux attach -t dev"
```

### 8.2 Full Day Development (8 Hours)

```bash
# Create sandbox with workspace
agentlab workspace create --name "my-workspace" --size 100G
agentlab sandbox new \
  --profile dev-template \
  --name "full-day-dev" \
  --workspace my-workspace \
  --ttl 8h \
  --keepalive

# SSH in and work in workspace
agentlab ssh <vmid>
cd /mnt/workspace
```

### 8.3 AI-Assisted Code Review

```bash
# Create sandbox
agentlab sandbox new --profile dev-template --name "code-review" --ttl 4h

# SSH in
agentlab ssh <vmid>

# Clone repo
git clone https://github.com/your/repo.git
cd repo

# Use Claude for code review
claude review --detailed src/

# Use Copilot for suggestions
gh copilot review src/

# Generate documentation
claude "Generate API documentation for src/api/"
```

### 8.4 Multi-Language Project

```bash
# Create sandbox
agentlab sandbox new --profile dev-template --name "multi-lang" --ttl 6h

# SSH in
agentlab ssh <vmid>

# Python backend
cd ~/projects/python
claude "Create a FastAPI backend with user endpoints"

# Node.js frontend
cd ~/projects/nodejs
claude "Create a React frontend that consumes the Python API"

# Test integration
cd ~/projects/general
python3 -m pytest ../python/tests/
npm test ../nodejs/tests/
```

### 8.5 Using VS Code Server

```bash
# Create sandbox
agentlab sandbox new --profile dev-template --name "vscode-session" --ttl 4h

# SSH in
agentlab ssh <vmid>

# Start Code Server (as dev user)
su - dev
code-server --bind-addr 0.0.0.0:8080

# Access in browser
# http://<sandbox-ip>:8080
# Password: agentlab
```

---

## Troubleshooting

### Template Creation Issues

**Problem:** Cloud-init doesn't run

**Solution:**
```bash
# Check cloud-init logs
cat /var/log/cloud-init-output.log
cat /var/log/cloud-init.log

# Re-run cloud-init
cloud-init clean
cloud-init init
```

**Problem:** Post-install script fails

**Solution:**
```bash
# Check script output
cat /var/log/post-install.log

# Run manually
bash /tmp/post-install.sh
```

### Runtime Issues

**Problem:** Claude Code CLI not working

**Solution:**
```bash
# Check API key
claude auth status

# Re-authenticate
claude auth login

# Verify installation
which claude
claude --version
```

**Problem:** Node.js version mismatch

**Solution:**
```bash
# Install specific version
nvm install 18
nvm use 18
```

**Problem:** Out of memory

**Solution:**
```bash
# Use interactive-dev profile (more memory)
agentlab sandbox new --profile interactive-dev --name "more-ram" --ttl 4h

# Or increase VM resources
qm set <vmid> -memory 12288
```

---

## Best Practices

1. **Test Template Thoroughly**
   - Create sandbox from template
   - Test each language environment
   - Test AI agents
   - Verify all tools work

2. **Keep Template Clean**
   - Remove unnecessary packages
   - Clear cache and temp files
   - Update regularly

3. **Document Customizations**
   - Keep track of changes
   - Maintain changelog
   - Version your templates

4. **Security Considerations**
   - Don't store API keys in template
   - Use secrets bundles for credentials
   - Rotate keys regularly
   - Update tools regularly

5. **Performance Optimization**
   - Pre-compile frequently used tools
   - Use caching where appropriate
   - Optimize disk I/O (use local storage)

---

## Quick Reference

### Create Sandbox with Dev Template

```bash
# Quick session
agentlab sandbox new --profile dev-template --name "dev-$(date +%H%M)" --ttl 2h

# Day session
agentlab sandbox new --profile dev-template --name "dev-$(date +%H%M)" --ttl 8h --keepalive

# With workspace
agentlab workspace create --name "work" --size 100G
agentlab sandbox new --profile dev-template --name "dev-$(date +%H%M)" --workspace work --ttl 8h
```

### Common Workflows

| Task | Command |
|------|---------|
| Start coding session | `tmux attach -t dev` (from SSH) |
| Use Claude Code | `claude "your prompt"` |
| Use GitHub Copilot | `gh copilot suggest` |
| New Python project | `mkdir proj && cd proj && python3 -m venv .venv` |
| New Node project | `npm create vite@latest my-app` |
| New Go project | `go mod init github.com/user/repo` |
| Run tests | `pytest` or `npm test` or `go test` |
| Format code | `black .` or `prettier --write .` or `go fmt ./` |

---

**Ready to Code!** 🚀

This template provides everything needed for productive AI-assisted coding sessions in AgentLab.
