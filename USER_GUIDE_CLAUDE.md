# Getting Started with Claude Code CLI in AgentLab

A complete user guide for starting to code with Claude Code CLI in an Ubuntu VM using AgentLab.

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [Understanding AgentLab Profiles](#2-understanding-agentlab-profiles)
3. [Creating a Coding Sandbox](#3-creating-a-coding-sandbox)
4. [Accessing Your Sandbox](#4-accessing-your-sandbox)
5. [Setting Up Your Development Environment](#5-setting-up-your-development-environment)
6. [Using Claude Code CLI](#6-using-claude-code-cli)
7. [Managing Your Session](#7-managing-your-session)
8. [Extending Your Lease](#8-extending-your-lease)
9. [Downloading Your Work](#9-downloading-your-work)
10. [Cleaning Up](#10-cleaning-up)
11. [Troubleshooting](#11-troubleshooting)
12. [Best Practices](#12-best-practices)

---

## 1. Prerequisites

### 1.1 On the Host (AgentLab Server)

Ensure you have:
- ✅ AgentLab installed and running (`systemctl status agentlabd`)
- ✅ At least one profile configured (`ls /etc/agentlab/profiles/`)
- ✅ Template VM created (`qm list | grep template`)
- ✅ Network configured (`ip addr show vmbr1`)
- ✅ Sufficient resources (CPU, memory, storage)

### 1.2 Required Tools

You'll need these on the host:
- `agentlab` CLI (built and installed)
- SSH client (`ssh` command)
- Your Git credentials (if cloning private repos)

### 1.3 On Your Sandbox (Ubuntu VM)

The template should include:
- Ubuntu (latest LTS recommended)
- SSH access enabled
- Common development tools (git, curl, wget, vim/nano)
- Python 3 or Node.js (depending on your needs)

---

## 2. Understanding AgentLab Profiles

AgentLab comes with pre-configured profiles. Choose one based on your needs:

| Profile | Cores | Memory | Storage | TTL | Use Case |
|---------|--------|---------|---------|-----|----------|
| `yolo-ephemeral` | 4 | 6GB | 3 hours | Short coding sessions, testing |
| `yolo-workspace` | 4 | 6GB | 8 hours | Longer sessions with workspace |
| `interactive-dev` | 6 | 8GB | 12 hours | Development with more resources |

**Key Differences:**
- **ephemeral**: No workspace, temporary storage only
- **workspace**: Persistent storage that can be reattached to new VMs
- **interactive**: More resources for intensive development

---

## 3. Creating a Coding Sandbox

### 3.1 Quick Start (Recommended)

For a short coding session, use the ephemeral profile:

```bash
# Create a 3-hour coding sandbox
agentlab sandbox new \
  --profile yolo-ephemeral \
  --name "claude-coding-$(date +%Y%m%d-%H%M)" \
  --ttl 3h
```

**Expected Output:**
```
VMID: 1012
Name: claude-coding-20260131-2200
Profile: yolo-ephemeral
State: REQUESTED
IP: -
Workspace: -
Keepalive: false
Lease Expires: 2026-02-01T01:00:00Z
Created At: 2026-01-31T22:00:00Z
```

### 3.2 Longer Development Session

For extended development, use the interactive profile with more resources:

```bash
# Create a 12-hour development sandbox
agentlab sandbox new \
  --profile interactive-dev \
  --name "dev-session-$(date +%Y%m%d-%H%M)" \
  --ttl 12h \
  --keepalive
```

**Note:** `--keepalive` prevents automatic lease expiration while you're working

### 3.3 Creating with a Job

If you want AgentLab to run a coding task automatically:

```bash
# Create a job with a coding task
agentlab job run \
  --repo https://github.com/yourusername/yourproject \
  --task "Implement the new user authentication feature using Claude Code CLI" \
  --profile yolo-ephemeral \
  --ttl 4h
```

### 3.4 Tracking Your Sandbox

```bash
# Check sandbox status
agentlab sandbox show 1012

# List all your sandboxes
agentlab sandbox list

# Watch the provisioning process
agentlab logs 1012 --follow
```

**State Transitions:**
```
REQUESTED → PROVISIONING → BOOTING → READY → RUNNING
```

Wait for state to reach **RUNNING** before proceeding.

---

## 4. Accessing Your Sandbox

### 4.1 Find Your Sandbox IP

```bash
# Get sandbox details with IP
agentlab sandbox show 1012
```

**Expected Output:**
```
VMID: 1012
Name: claude-coding-20260131-2200
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.153
Workspace: -
Keepalive: false
Lease Expires: 2026-02-01T01:00:00Z
```

### 4.2 SSH into Your Sandbox

```bash
# Basic SSH (uses root user)
agentlab ssh 1012

# With custom user
agentlab ssh 1012 --user ubuntu

# With custom SSH key
agentlab ssh 1012 --identity ~/.ssh/my_key
```

**Expected Connection:**
```
root@claude-coding-20260131-2200:~#
```

### 4.3 First Steps Inside the Sandbox

```bash
# Check OS version
cat /etc/os-release

# Update package lists
apt update

# Check installed tools
which git node python3 curl wget

# Check disk space
df -h

# Check memory
free -h

# Check available CPUs
nproc
```

---

## 5. Setting Up Your Development Environment

### 5.1 Installing Development Tools

#### For Python Development

```bash
# Install Python development tools
apt install -y \
  python3-pip \
  python3-venv \
  python3-dev \
  build-essential

# Create a virtual environment
python3 -m venv /venv
source /venv/bin/activate

# Install common packages
pip install --upgrade pip
pip install pytest black flake8 mypy
```

#### For Node.js Development

```bash
# Install Node.js (latest LTS)
curl -fsSL https://deb.nodesource.com/setup_lts.x | bash -
apt install -y nodejs

# Verify installation
node --version
npm --version

# Install common tools
npm install -g typescript eslint prettier
```

#### For Go Development

```bash
# Install Go
wget https://go.dev/dl/go1.21.5.linux-amd64.tar.gz
tar -C /usr/local -xzf go1.21.5.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# Verify
go version
```

### 5.2 Installing Claude Code CLI

```bash
# Using npm (if Node.js is available)
npm install -g @anthropic-ai/claude-code

# Or using pip (if Python is available)
pip install anthropic

# Or using the official installer
curl -fsSL https://install.anthropic.com | sh
```

### 5.3 Configuring Claude Code CLI

```bash
# Initialize Claude Code CLI configuration
claude init

# This will prompt for:
# - API key (get from https://console.anthropic.com)
# - Default model (claude-3-5-sonnet, claude-3-opus, etc.)
# - Editor preferences (vim, emacs, nano, etc.)
# - Code style preferences
```

**Create API Key:**
1. Visit https://console.anthropic.com/
2. Sign in or create account
3. Navigate to API Keys
4. Create new key
5. Save key to `~/.claude/config.yml`

### 5.4 Setting Up Git

```bash
# Configure git
git config --global user.name "Your Name"
git config --global user.email "your.email@example.com"
git config --global init.defaultBranch main
git config --global credential.helper store

# If using SSH keys
ssh-keygen -t ed25519 -C "your.email@example.com"
cat ~/.ssh/id_ed25519.pub
# Add to your GitHub/GitLab SSH keys
```

---

## 6. Using Claude Code CLI

### 6.1 Starting a Coding Session

```bash
# Navigate to your project directory
cd /work/repo  # or wherever you cloned your repo

# Start Claude Code CLI
claude

# Or with a specific task
claude "Implement the user authentication feature"
```

### 6.2 Common Claude Code CLI Commands

#### Basic Workflow

```bash
# Explain code
claude explain src/auth.py

# Generate code
claude "Create a REST API endpoint for user registration"

# Review code
claude review src/

# Refactor code
claude "Refactor the authentication module to be more testable"

# Debug with Claude
claude debug "The user login fails with a 500 error"

# Write tests
claude "Write unit tests for the auth module"
```

#### Interactive Session

```bash
# Start interactive mode
claude --interactive

# Then type natural language commands:
> "What does the auth function do?"
> "Add error handling to the database calls"
> "Generate documentation for this API"
> "Optimize this query"
```

#### File Operations

```bash
# Edit a file
claude edit src/auth.py "Add rate limiting"

# Create a new file
claude create src/middleware.py "Add logging middleware"

# Multiple files
claude create \
  src/models/user.py \
  src/models/session.py \
  "Create user and session models"
```

### 6.3 Project-Specific Workflows

#### Example: Building a REST API

```bash
# Initialize project structure
claude "Create a Flask REST API project structure with user endpoints"

# Implement authentication
claude "Implement JWT-based authentication for the API"

# Add database models
claude "Create SQLAlchemy models for User and Session tables"

# Write API endpoints
claude "Implement POST /api/auth/register endpoint"
claude "Implement POST /api/auth/login endpoint"
claude "Implement GET /api/auth/me endpoint"

# Add validation
claude "Add input validation using marshmallow"

# Write tests
claude "Write pytest tests for all auth endpoints"
```

#### Example: Data Science Project

```bash
# Setup environment
claude "Create a Python data science environment with pandas, numpy, scikit-learn"

# Load data
claude "Create a script to load the dataset from CSV"

# EDA
claude "Perform exploratory data analysis and generate visualizations"

# Model training
claude "Train a random forest model on the dataset"

# Evaluation
claude "Create evaluation metrics and generate a classification report"
```

#### Example: Frontend Development

```bash
# Setup React project
claude "Create a React project with TypeScript using Vite"

# Implement components
claude "Create a reusable Button component with variants"

# State management
claude "Add Redux store with user authentication state"

# API integration
claude "Create React Query hooks for API communication"

# Styling
claude "Style the auth form using Tailwind CSS"
```

### 6.4 Best Practices with Claude Code CLI

#### 1. Be Specific

```bash
# Good: Specific, actionable
claude "Add error handling to the database connection in src/db.py"

# Avoid: Too vague
claude "Fix the database issues"
```

#### 2. Provide Context

```bash
# Good: With context
claude "I'm getting a 500 error when POSTing to /api/auth/login.
The error occurs after 30 seconds. Check the auth module and suggest fixes."

# Avoid: No context
claude "Fix the login error"
```

#### 3. Ask for Explanations

```bash
# Understand what Claude is doing
claude explain --step-by-step "How does the JWT validation work?"

# Learn about code
claude review --detailed src/auth.py

# Get suggestions
claude "Review the auth module and suggest improvements for security"
```

#### 4. Iterate Gradually

```bash
# Start small
claude "Create a basic login form"

# Add features incrementally
claude "Add email validation to the login form"

# Then test
claude "Write a test for the login form validation"
```

---

## 7. Managing Your Session

### 7.1 Monitoring Sandbox Health

```bash
# On host: Check sandbox status
agentlab sandbox show 1012

# In sandbox: Check system resources
htop  # if available
df -h
free -h

# Check disk usage
du -sh /work/repo
```

### 7.2 Viewing Logs

```bash
# On host: View sandbox events
agentlab logs 1012 --follow

# In sandbox: View application logs
tail -f /var/log/app.log
journalctl -f -u your-service
```

### 7.3 Saving Your Work Frequently

```bash
# In sandbox: Commit frequently
git add .
git commit -m "WIP: implementing auth feature"

# Push to remote (if configured)
git push origin feature/your-branch

# Or download artifacts from host
agentlab job artifacts download <job-id> --out ~/backups
```

---

## 8. Extending Your Lease

### 8.1 Check Remaining Lease Time

```bash
# Check when your sandbox expires
agentlab sandbox show 1012

# Look for:
# Lease Expires: 2026-02-01T01:00:00Z
```

### 8.2 Renew Your Lease

```bash
# Extend lease by 2 hours
agentlab sandbox lease renew --ttl 2h 1012

# Extend by 120 minutes
agentlab sandbox lease renew --ttl 120 1012

# Extend for 1 day
agentlab sandbox lease renew --ttl 24h 1012
```

**Important:** Flags must come before VMID!

**Valid States for Renewal:**
- RUNNING ✅
- STOPPED ✅
- READY ✅

**Invalid States for Renewal:**
- TIMEOUT ❌
- DESTROYED ❌
- FAILED ❌

### 8.3 Using Keepalive

```bash
# Create sandbox with keepalive enabled
agentlab sandbox new \
  --profile yolo-workspace \
  --name "long-session" \
  --ttl 24h \
  --keepalive

# This prevents automatic expiration while active
# You still need to renew manually if you want to extend beyond TTL
```

---

## 9. Downloading Your Work

### 9.1 Using Artifacts

If you ran a job, artifacts are automatically uploaded:

```bash
# List artifacts
agentlab job artifacts <job-id>

# Download specific artifact
agentlab job artifacts download <job-id> --path /path/to/file

# Download latest bundle
agentlab job artifacts download <job-id> --bundle

# Download to specific directory
agentlab job artifacts download <job-id> --out ~/my-backups/
```

### 9.2 Manual Backup via SSH

```bash
# From host: Copy files from sandbox
scp -r root@10.77.0.153:/work/repo ~/my-backups/

# Or tar and transfer
ssh root@10.77.0.153 "cd /work && tar czf - repo" > repo.tar.gz

# Or use rsync for incremental backups
rsync -avz --progress root@10.77.0.153:/work/repo/ ~/my-backups/
```

### 9.3 Using Workspace (if configured)

```bash
# Create a workspace
agentlab workspace create \
  --name "my-dev-workspace" \
  --size 80G \
  --storage local-zfs

# Attach to sandbox
agentlab workspace attach my-dev-workspace 1012

# Work in workspace (mounted in sandbox)
cd /mnt/workspace

# Detach when done
agentlab workspace detach my-dev-workspace

# Reattach to new sandbox
agentlab workspace rebind my-dev-workspace --profile yolo-workspace
```

---

## 10. Cleaning Up

### 10.1 Destroy Your Sandbox

```bash
# Normal destroy (only works in STOPPED or DESTROYED state)
agentlab sandbox destroy 1012

# Force destroy (bypasses state restrictions)
agentlab sandbox destroy --force 1012
```

### 10.2 Cleanup Orphaned Entries

```bash
# Remove all orphaned TIMEOUT sandboxes
agentlab sandbox prune

# Output: pruned 5 sandbox(es)
```

### 10.3 Clean Up Workspace

```bash
# If you used a workspace, you might want to clean it
# Option 1: Keep workspace for next session
# Option 2: Delete workspace
qm destroy <workspace-vmid>

# Or detach and keep it for later
agentlab workspace detach my-dev-workspace
```

---

## 11. Troubleshooting

### 11.1 Cannot Connect to Sandbox

**Problem:** SSH connection fails

**Checklist:**
```bash
# 1. Is sandbox running?
agentlab sandbox show 1012
# Should be in RUNNING state

# 2. What's the IP?
# Copy the IP address from output

# 3. Test connectivity from host
ping 10.77.0.153

# 4. Check firewall
iptables -L -n | grep 10.77

# 5. Try with verbose SSH
ssh -vvv root@10.77.0.153
```

**Solutions:**
- Wait for provisioning to complete
- Check firewall rules
- Verify network bridge is up
- Try `agentlab ssh 1012` instead of manual ssh

### 11.2 Sandbox Times Out Too Quickly

**Problem:** Sandbox expires before you're done

**Solutions:**
```bash
# Use longer TTL
agentlab sandbox new --profile yolo-workspace --ttl 24h

# Use keepalive
agentlab sandbox new --profile yolo-workspace --ttl 24h --keepalive

# Renew lease
agentlab sandbox lease renew --ttl 4h 1012

# Check current status
agentlab sandbox show 1012
```

### 11.3 Out of Resources

**Problem:** Sandbox is slow or crashes

**Checklist:**
```bash
# In sandbox: Check resources
free -h
df -h
top

# On host: Check VM allocation
qm config 1012 | grep -E "cores|memory|net"

# Check if VM is on the same node as templates
```

**Solutions:**
- Use `interactive-dev` profile for more resources
- Close unnecessary applications
- Use a workspace to offload files
- Check for memory leaks

### 11.4 Claude Code CLI Issues

**Problem:** Claude Code CLI not working

**Solutions:**
```bash
# Check installation
which claude
claude --version

# Check API key
cat ~/.claude/config.yml
# Should contain: api_key: sk-ant-...

# Test API key
curl https://api.anthropic.com/v1/messages \
  -H "x-api-key: YOUR_KEY" \
  -H "anthropic-version: 2023-06-01"

# Reinstall if needed
pip uninstall anthropic && pip install anthropic
```

---

## 12. Best Practices

### 12.1 For Coding Sessions

1. **Start with appropriate TTL**
   - Short tasks: 2-3 hours
   - Medium projects: 4-8 hours
   - Long sessions: 12-24 hours

2. **Use keepalive for extended work**
   - Prevents automatic expiration
   - Remember to renew if needed

3. **Commit frequently**
   - Every significant change
   - Before switching contexts
   - Before destroying sandbox

4. **Test incrementally**
   - Don't build massive changes
   - Test small pieces
   - Get feedback from Claude Code CLI

5. **Monitor resources**
   - Check disk usage regularly
   - Watch for memory issues
   - Clean up temp files

### 12.2 For Using Claude Code CLI

1. **Be specific and clear**
   - Describe exactly what you want
   - Provide context when possible
   - Reference specific files or functions

2. **Iterate gradually**
   - Build small pieces
   - Test each piece
   - Ask Claude to review

3. **Leverage explanations**
   - Ask "why" to understand
   - Request "how" for implementation
   - Use `explain` command

4. **Use version control wisely**
   - Create branches for features
   - Write descriptive commits
   - Use Claude to write commit messages

5. **Document as you go**
   - Ask Claude to document code
   - Generate README files
   - Create API documentation

### 12.3 For Sandbox Management

1. **Name your sandboxes clearly**
   ```bash
   agentlab sandbox new \
     --name "feature-auth-development" \
     --profile yolo-workspace
   ```

2. **Keep track of your VMIDs**
   - Use a note-taking app
   - Save in a text file:
     ```bash
     echo "1012: auth feature dev" >> ~/agentlab-sandboxes.txt
     ```

3. **Clean up regularly**
   - Run `agentlab sandbox prune` periodically
   - Destroy unused sandboxes
   - Keep your list current

4. **Use workspaces for ongoing projects**
   - Perfect for multi-day development
   - Preserves your work between sessions
   - Easy to reattach to new sandbox

---

## Quick Reference Commands

### Common Workflow

```bash
# 1. Create sandbox
agentlab sandbox new --profile yolo-ephemeral --name "dev-$(date +%Y%m%d)" --ttl 4h

# 2. Wait for RUNNING state
watch -n 5 'agentlab sandbox show $(agentlab sandbox list | grep "dev-" | head -1 | awk "{print \$1}")'

# 3. SSH in
agentlab ssh <vmid>

# 4. Setup environment (first time only)
# Install tools, Claude Code CLI, etc.

# 5. Work with Claude
claude "Your task here"

# 6. Save work
git add . && git commit -m "Progress"

# 7. Extend if needed
agentlab sandbox lease renew --ttl 2h <vmid>

# 8. Download artifacts
agentlab job artifacts download <job-id>

# 9. Cleanup
agentlab sandbox destroy <vmid>
```

### Command Cheatsheet

| Action | Command |
|--------|---------|
| List sandboxes | `agentlab sandbox list` |
| Show sandbox details | `agentlab sandbox show <vmid>` |
| Create sandbox | `agentlab sandbox new --profile <profile> --name <name> --ttl <ttl>` |
| SSH into sandbox | `agentlab ssh <vmid>` |
| View logs | `agentlab logs <vmid>` |
| Renew lease | `agentlab sandbox lease renew --ttl <ttl> <vmid>` |
| Destroy sandbox | `agentlab sandbox destroy <vmid>` |
| Force destroy | `agentlab sandbox destroy --force <vmid>` |
| Prune orphaned | `agentlab sandbox prune` |

---

## Example Sessions

### Example 1: Quick Bug Fix (1 Hour)

```bash
# Create sandbox
agentlab sandbox new --profile yolo-ephemeral --name "bugfix-auth" --ttl 2h

# Wait for RUNNING state, then SSH in
agentlab ssh 1012

# In sandbox:
git clone https://github.com/your/repo
cd repo
git checkout -b fix/login-bug

# Work with Claude
claude "Fix the login form validation issue described in issue #123"

# Test
pytest tests/test_auth.py

# Commit and push
git add .
git commit -m "Fix: login form validation"
git push origin fix/login-bug

# Cleanup
exit
agentlab sandbox destroy 1012
```

### Example 2: Feature Development (4 Hours)

```bash
# Create sandbox with workspace
agentlab workspace create --name "user-auth" --size 50G
agentlab sandbox new --profile yolo-workspace --name "feature-auth" --workspace user-auth --ttl 6h --keepalive

# SSH in and setup
agentlab ssh 1013
cd /mnt/workspace

# Initialize project
claude "Create a Flask project structure for user authentication"

# Implement iteratively
claude "Create user model with SQLAlchemy"
claude "Implement user registration endpoint"
claude "Implement user login with JWT"
claude "Add password reset functionality"

# Test each feature
pytest tests/

# Save work
git add .
git commit -m "Feature: user authentication system"

# Renew if needed (from host)
agentlab sandbox lease renew --ttl 4h 1013

# Download artifacts
agentlab job artifacts download <job-id> --out ~/backups/

# Keep workspace, destroy sandbox
agentlab workspace detach user-auth
agentlab sandbox destroy 1013

# Next day: Reattach workspace
agentlab sandbox new --profile yolo-workspace --name "continue-auth" --workspace user-auth --ttl 8h
```

### Example 3: Code Review Session (2 Hours)

```bash
# Create ephemeral sandbox
agentlab sandbox new --profile yolo-ephemeral --name "code-review" --ttl 3h

# SSH in
agentlab ssh 1014

# Clone repo
git clone https://github.com/your/repo
cd repo

# Code review with Claude
claude review --detailed src/

# Get suggestions
claude "Review the authentication module and suggest security improvements"

# Generate documentation
claude "Generate API documentation for src/api/"

# Output results to file
claude "Generate a code review report and save to /work/code-review.md"

# Download
scp root@10.77.0.154:/work/code-review.md ~/reviews/

# Cleanup
agentlab sandbox destroy 1014
```

---

## Getting Help

### Documentation

- AgentLab README: `README.md`
- Troubleshooting guide: `docs/troubleshooting.md`
- API documentation: `docs/api.md`
- Runbook: `docs/runbook.md`

### Support

When issues occur:

1. Check logs:
   ```bash
   journalctl -u agentlabd -n 100
   ```

2. Check sandbox status:
   ```bash
   agentlab sandbox show <vmid>
   ```

3. Check resources:
   ```bash
   qm config <vmid>
   ```

4. Collect diagnostics:
   ```bash
   agentlab --version > /tmp/agentlab-info.txt
   qm list >> /tmp/agentlab-info.txt
   ```

---

**Happy coding with Claude Code CLI and AgentLab!** 🚀

Remember: Your sandboxes are temporary. Save your work frequently and clean up when you're done.
