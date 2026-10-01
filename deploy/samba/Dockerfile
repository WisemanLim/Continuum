# Continuum SMB access point (FR-A01~A05): Samba + mergerfs. The Controller renders /config
# (smb.conf, start.sh, secret) and bind-mounts each disk's data root at /disks/<disk_id>.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends samba samba-vfs-modules mergerfs fuse3 \
 && rm -rf /var/lib/apt/lists/* \
 && sed -i 's/^#user_allow_other/user_allow_other/' /etc/fuse.conf
EXPOSE 445
ENTRYPOINT ["/bin/sh", "/config/start.sh"]
