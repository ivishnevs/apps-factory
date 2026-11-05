#!/bin/sh
# Creates Dex's database on first start when Dex keeps its state in Postgres (the hosted image, deploy/dex/Dockerfile):
# a hosted Postgres (Railway) has no init hook and no public address, so the image does it from the private network.
# DEX_DB_CREATE=0 skips it (the database exists or the user may not create databases). Then runs Dex as usual.
set -eu
if [ "${DEX_STORAGE:-postgres}" != "memory" ] && [ "${DEX_DB_CREATE:-1}" != "0" ]; then
  export PGHOST="${DEX_DB_HOST:-postgres}" PGPORT="${DEX_DB_PORT:-5432}" PGUSER="${DEX_DB_USER:-factory}"
  export PGPASSWORD="${DEX_DB_PASSWORD:-${POSTGRES_PASSWORD:-}}" PGSSLMODE="${DEX_DB_SSLMODE:-disable}"
  name="${DEX_DB_NAME:-dex}"
  admin="${DEX_DB_ADMIN_DATABASE:-postgres}"
  case "$name" in *[!a-zA-Z0-9_]*) echo "ensure-db: DEX_DB_NAME must be letters, digits and _" >&2; exit 1;; esac
  i=0
  until psql -d "$admin" -tAc "SELECT 1" >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -ge 30 ]; then echo "ensure-db: Postgres at $PGHOST:$PGPORT is not reachable" >&2; exit 1; fi
    sleep 2
  done
  if ! psql -d "$admin" -tAc "SELECT 1 FROM pg_database WHERE datname = '$name'" | grep -q 1; then
    psql -d "$admin" -c "CREATE DATABASE \"$name\"" >/dev/null
    echo "ensure-db: created database $name"
  fi
fi
exec /usr/local/bin/docker-entrypoint "$@"
