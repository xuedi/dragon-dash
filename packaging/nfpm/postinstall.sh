#!/bin/sh
# Runs after the package is installed on deb/rpm/Arch. It creates the dedicated
# system user, seeds the configuration file if there is not one already, and
# prints the next steps. The unit ships disabled: credentials go in first, the
# operator enables the service afterwards.
set -e

CONF_DIR=/etc/dragon-dash
CONF=$CONF_DIR/dragon-dash.env
TEMPLATE=/usr/share/dragon-dash/dragon-dash.env.example

nologin_shell=/usr/sbin/nologin
[ -x "$nologin_shell" ] || nologin_shell=/sbin/nologin
[ -x "$nologin_shell" ] || nologin_shell=/bin/false

if ! getent group dragon-dash >/dev/null 2>&1; then
	groupadd --system dragon-dash 2>/dev/null || addgroup --system dragon-dash 2>/dev/null || true
fi
if ! getent passwd dragon-dash >/dev/null 2>&1; then
	useradd --system --gid dragon-dash --home-dir / --no-create-home \
		--shell "$nologin_shell" --comment "dragon-dash dashboard" dragon-dash 2>/dev/null ||
		adduser --system --ingroup dragon-dash --home / --no-create-home \
			--shell "$nologin_shell" dragon-dash 2>/dev/null || true
fi

mkdir -p "$CONF_DIR"
chmod 0750 "$CONF_DIR"
chown root:dragon-dash "$CONF_DIR" 2>/dev/null || true

# Never overwrite an existing configuration, an upgrade must not drop credentials.
if [ ! -f "$CONF" ] && [ -f "$TEMPLATE" ]; then
	cp "$TEMPLATE" "$CONF"
fi
if [ -f "$CONF" ]; then
	# The FRITZ!Box password lives here, so it is readable by the service and
	# by root, and by nobody else.
	chmod 0640 "$CONF"
	chown root:dragon-dash "$CONF" 2>/dev/null || true
fi

command -v systemctl >/dev/null 2>&1 && systemctl daemon-reload >/dev/null 2>&1 || true

cat <<EOF

dragon-dash is installed. The systemd unit is present but disabled. To finish:

  1. edit $CONF
       DD_CORE_ADDR           where to listen, :80 for the default HTTP port
       DD_CORE_TLS_*          certificate, key and :443 to serve HTTPS as well
       DD_CORE_PROMETHEUS_URL where the metrics are read from
       DD_SYSTEM_FRITZHOME_*  FRITZ!Box host and credentials
  2. sudo systemctl enable --now dragon-dash

It reads from Prometheus and writes nothing, so it needs a Prometheus reachable
at DD_CORE_PROMETHEUS_URL to show anything.
EOF

exit 0
