#!/bin/sh
# Runs after the package is installed on deb/rpm/Arch. It creates the dedicated
# system user, seeds the configuration file if there is not one already, and
# prints the next steps. The unit ships disabled: credentials go in first, the
# operator enables the service afterwards.
set -e

CONF_DIR=/etc/armdash
CONF=$CONF_DIR/armdash.env
TEMPLATE=/usr/share/armdash/armdash.env.example

nologin_shell=/usr/sbin/nologin
[ -x "$nologin_shell" ] || nologin_shell=/sbin/nologin
[ -x "$nologin_shell" ] || nologin_shell=/bin/false

if ! getent group armdash >/dev/null 2>&1; then
	groupadd --system armdash 2>/dev/null || addgroup --system armdash 2>/dev/null || true
fi
if ! getent passwd armdash >/dev/null 2>&1; then
	useradd --system --gid armdash --home-dir / --no-create-home \
		--shell "$nologin_shell" --comment "armdash dashboard" armdash 2>/dev/null ||
		adduser --system --ingroup armdash --home / --no-create-home \
			--shell "$nologin_shell" armdash 2>/dev/null || true
fi

mkdir -p "$CONF_DIR"
chmod 0750 "$CONF_DIR"
chown root:armdash "$CONF_DIR" 2>/dev/null || true

# Never overwrite an existing configuration, an upgrade must not drop credentials.
if [ ! -f "$CONF" ] && [ -f "$TEMPLATE" ]; then
	cp "$TEMPLATE" "$CONF"
fi
if [ -f "$CONF" ]; then
	# The FRITZ!Box password lives here, so it is readable by the service and
	# by root, and by nobody else.
	chmod 0640 "$CONF"
	chown root:armdash "$CONF" 2>/dev/null || true
fi

command -v systemctl >/dev/null 2>&1 && systemctl daemon-reload >/dev/null 2>&1 || true

cat <<EOF

armdash is installed. The systemd unit is present but disabled. To finish:

  1. edit $CONF
       AD_CORE_ADDR           where to listen, :80 for the default HTTP port
       AD_CORE_TLS_*          certificate, key and :443 to serve HTTPS as well
       AD_CORE_PROMETHEUS_URL where the metrics are read from
       AD_SYSTEM_FRITZHOME_*  FRITZ!Box host and credentials
  2. armdash passwd
       prints AD_CORE_AUTH_USER and AD_CORE_AUTH_PASSWORD_HASH for $CONF,
       the login that uploads a floor plan and places devices; without it
       nothing can be changed
  3. sudo systemctl enable --now armdash

Every metric is read from Prometheus, so it needs one reachable at
AD_CORE_PROMETHEUS_URL to show anything.
EOF

exit 0
