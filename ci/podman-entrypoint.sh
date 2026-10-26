#!/usr/bin/env bash
# Entrypoint for rootless podman in OpenShift CI pods.
# OpenShift runs containers as an arbitrary UID with GID 0 supplementary.
# This script creates the passwd/subuid entries that rootless podman needs.

if [ ! -d "${HOME}" ]; then
  mkdir -p "${HOME}"
fi

# Podman storage and registry configuration
if [ ! -d "${HOME}/.config/containers" ]; then
  mkdir -p "${HOME}/.config/containers"
  if [ -c "/dev/fuse" ] && [ -f "/usr/bin/fuse-overlayfs" ]; then
    cat > "${HOME}/.config/containers/storage.conf" <<'EOF'
[storage]
driver = "overlay"
graphroot = "/tmp/graphroot"
[storage.options.overlay]
mount_program = "/usr/bin/fuse-overlayfs"
EOF
  else
    cat > "${HOME}/.config/containers/storage.conf" <<'EOF'
[storage]
driver = "vfs"
EOF
  fi
fi

# Create passwd entry for the arbitrary UID if missing
if ! whoami &>/dev/null; then
  if [ -w /etc/passwd ]; then
    echo "${USER_NAME:-user}:x:$(id -u):0:${USER_NAME:-user} user:${HOME}:/bin/bash" >> /etc/passwd
    echo "${USER_NAME:-user}:x:$(id -u):" >> /etc/group
  fi
fi

# Create subuid/subgid ranges for rootless podman.
# Start just above the running UID to avoid overlap (the third field is the
# count, not an end ID). Works for any UID, including OpenShift's arbitrary
# high UIDs like 1000650000.
USER=$(whoami)
START_ID=$(( $(id -u) + 1 ))
echo "${USER}:${START_ID}:65536" > /etc/subuid
echo "${USER}:${START_ID}:65536" > /etc/subgid

exec "$@"
