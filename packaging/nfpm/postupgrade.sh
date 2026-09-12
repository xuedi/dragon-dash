#!/bin/sh
# Runs after pacman upgrades the package, deb and rpm upgrades go through
# postinstall.sh. A running service moves to the new binary, a stopped one stays
# stopped.
if command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload >/dev/null 2>&1 || true
	systemctl try-restart armdash >/dev/null 2>&1 || true
fi

exit 0
