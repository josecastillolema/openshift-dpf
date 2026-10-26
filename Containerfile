FROM docker.io/alpine/helm:3 as helm-cli
FROM quay.io/openshift/origin-cli:4.22 as oc-cli
FROM registry.access.redhat.com/ubi10/ubi:latest

LABEL org.opencontainers.image.authors="Red Hat Ecosystem Engineering"

USER root

# Copying oc binary
COPY --from=oc-cli /usr/bin/oc /usr/bin/oc
RUN ln -s /usr/bin/oc /usr/bin/kubectl

RUN dnf install -y --allowerasing findutils gettext git golang jq make openssh-clients podman python3-devel python3-pip rsync shadow-utils && \
    rpm --import https://www.centos.org/keys/RPM-GPG-KEY-CentOS-Official-SHA256 && \
    dnf install -y --repofrompath='cs10-appstream,https://mirror.stream.centos.org/10-stream/AppStream/$basearch/os/' \
      --setopt=cs10-appstream.gpgkey=https://www.centos.org/keys/RPM-GPG-KEY-CentOS-Official-SHA256 \
      libvirt-client virt-install && \
    dnf clean all

# Copying helm binary
COPY --from=helm-cli /usr/bin/helm /usr/bin/helm

# Install aicli
RUN pip3 install aicli

# Default to vfs storage driver for nested container builds (overlay over
# overlayfs is unsupported without fuse-overlayfs).
RUN sed -i 's/^driver = "overlay"/driver = "vfs"/' /usr/share/containers/storage.conf

# Get the source code in there
WORKDIR /root/dpf-ci

COPY . .

# Make workspace writable for OpenShift's arbitrary user IDs
# Note: SSH keys should NOT be in the image - they're mounted at runtime from secrets
RUN chmod 777 /root/dpf-ci -R

# Rootless podman setup for OpenShift's arbitrary UID (runs as non-root
# with GID 0 supplementary). Follows the nested-podman CI image pattern.
RUN chmod g=u /etc/passwd /etc/group && \
    setcap cap_setuid+ep /usr/bin/newuidmap && \
    setcap cap_setgid+ep /usr/bin/newgidmap && \
    touch /etc/subuid /etc/subgid && \
    chmod g=u /etc/subuid /etc/subgid

COPY ci/podman-entrypoint.sh /podman-entrypoint.sh
RUN chmod +rx /podman-entrypoint.sh

ENTRYPOINT ["/podman-entrypoint.sh"]
