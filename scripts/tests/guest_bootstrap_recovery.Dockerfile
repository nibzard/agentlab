FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash coreutils openssl curl jq ca-certificates util-linux \
    && rm -rf /var/lib/apt/lists/*
