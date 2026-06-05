#!/bin/sh
set -eu

runtime_uid="${MIVIA_RUNTIME_UID:-${MIVIA_AUTOMATION_UID:-10001}}"
runtime_gid="${MIVIA_RUNTIME_GID:-${MIVIA_AUTOMATION_GID:-10001}}"
runtime_user="${MIVIA_RUNTIME_USER:-mivia-runtime}"
runtime_group="${MIVIA_RUNTIME_GROUP:-mivia-runtime}"
runtime_home="${MIVIA_RUNTIME_HOME:-${HOME:-/home/mivia}}"

if [ "$(id -u)" != "0" ]; then
	exec "$@"
fi

if ! getent group "$runtime_gid" >/dev/null 2>&1; then
	echo "${runtime_group}:x:${runtime_gid}:" >>/etc/group
fi

if ! getent passwd "$runtime_uid" >/dev/null 2>&1; then
	group_name="$(getent group "$runtime_gid" | cut -d: -f1)"
	echo "${runtime_user}:x:${runtime_uid}:${runtime_gid}:Mivia runtime:${runtime_home}:/bin/sh" >>/etc/passwd
fi

mkdir -p "$runtime_home"
chown "$runtime_uid:$runtime_gid" "$runtime_home" 2>/dev/null || true
export HOME="$runtime_home"

exec setpriv --reuid "$runtime_uid" --regid "$runtime_gid" --init-groups "$@"
