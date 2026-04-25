# AgentLab Base Agent Image
# Ubuntu 22.04 with common development tools pre-installed.
# This image serves as the foundation for all agent-ready sandboxes.
#
# Build:
#   docker build -t agentlab/agent-base:latest .
#
# Use:
#   agentlab sandbox new --type=docker --image=agentlab/agent-base:latest --profile=agent-base

FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8

# Install base development tools and dependencies.
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    ca-certificates \
    curl \
    git \
    gnupg \
    jq \
    less \
    locales \
    man-db \
    net-tools \
    openssh-server \
    pkg-config \
    python3 \
    python3-pip \
    python3-venv \
    ripgrep \
    rsync \
    socat \
    sudo \
    tmux \
    unzip \
    vim \
    wget \
    zsh \
    && rm -rf /var/lib/apt/lists/*

# Configure locale.
RUN locale-gen en_US.UTF-8

# Install Node.js 20 LTS.
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install Go.
ARG GO_VERSION=1.22.5
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xzf -
ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/root/go"
ENV PATH="${GOPATH}/bin:${PATH}"

# Install common CLI tools.
RUN curl -fsSL https://github.com/sharkdp/fd/releases/download/v10.1.0/fd_10.1.0_amd64.deb -o /tmp/fd.deb \
    && dpkg -i /tmp/fd.deb \
    && rm /tmp/fd.deb

# Create workspace directory.
RUN mkdir -p /workspace && chmod 777 /workspace
WORKDIR /workspace

# Configure SSH for remote access.
RUN mkdir -p /run/sshd \
    && sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin yes/' /etc/ssh/sshd_config \
    && sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config

# Copy agent startup scripts.
COPY agent-entrypoint.sh /usr/local/bin/agent-entrypoint.sh
RUN chmod +x /usr/local/bin/agent-entrypoint.sh

# Copy agentlab guest client.
COPY agentlab-guest /usr/local/bin/agentlab-guest
RUN chmod +x /usr/local/bin/agentlab-guest

EXPOSE 22
ENTRYPOINT ["/usr/local/bin/agent-entrypoint.sh"]
CMD ["/usr/sbin/sshd", "-D"]
