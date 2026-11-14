#!/bin/sh
# bootstrap-toolchain.sh — restore Go/Rust/Python tooling after a sandbox wipe.
#
# Sandboxes wipe /tmp and ~/.local on recycle; /mnt/agents persists but has no
# exec bits or symlinks. So toolchain archives live in
# /mnt/agents/persist/toolchains/ and this script restores them into /tmp
# (exec-capable) in seconds, without re-downloading from the internet.
#
# Usage:  sh scripts/bootstrap-toolchain.sh
# Then:   export PATH=/tmp/go/bin:/tmp/rust/cargo/bin:$PATH
set -eu

CACHE=/mnt/agents/persist/toolchains

# --- Go ---------------------------------------------------------------------
if [ ! -x /tmp/go/bin/go ]; then
    if [ -f "$CACHE/go-toolchain.tar.gz" ]; then
        echo "restoring Go from cache..."
        tar -xzf "$CACHE/go-toolchain.tar.gz" -C /tmp
    else
        echo "downloading Go 1.27.4..."
        python3 -c "import urllib.request; urllib.request.urlretrieve('https://go.dev/dl/go1.27.4.linux-amd64.tar.gz','/tmp/go.tgz')"
        mkdir -p /tmp/go && tar -xzf /tmp/go.tgz -C /tmp
    fi
fi
/tmp/go/bin/go version

# --- Rust -------------------------------------------------------------------
if [ ! -x /tmp/rust/cargo/bin/rustc ]; then
    if [ -f "$CACHE/rust-toolchain.tar.gz.part-aa" ]; then
        echo "restoring Rust from cache..."
        cat "$CACHE"/rust-toolchain.tar.gz.part-* > /tmp/rust-toolchain.tar.gz
        tar -xzf /tmp/rust-toolchain.tar.gz -C /tmp
    else
        echo "installing Rust via rustup..."
        RUSTUP_HOME=/tmp/rust/rustup CARGO_HOME=/tmp/rust/cargo \
            sh -c "curl -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain stable"
        /tmp/rust/cargo/bin/rustup component add clippy rustfmt
    fi
fi
RUSTUP_HOME=/tmp/rust/rustup CARGO_HOME=/tmp/rust/cargo /tmp/rust/cargo/bin/rustc --version

# --- Python test/build deps --------------------------------------------------
if ! python3 -m pytest --version >/dev/null 2>&1; then
    echo "restoring Python deps from wheel cache..."
    if [ -d "$CACHE/pywheels" ]; then
        pip install -q --no-index --find-links "$CACHE/pywheels" \
            pytest pytest-asyncio httpx fastapi uvicorn pydantic
    else
        pip install -q pytest pytest-asyncio httpx fastapi uvicorn pydantic
    fi
fi
python3 -m pytest --version | head -1

echo "toolchain ready."
echo "export PATH=/tmp/go/bin:/tmp/rust/cargo/bin:\$PATH"
echo "export RUSTUP_HOME=/tmp/rust/rustup CARGO_HOME=/tmp/rust/cargo"
# /mnt has no exec bits: cargo must build/run test binaries off-mount
echo "export CARGO_TARGET_DIR=/tmp/rust-target"
