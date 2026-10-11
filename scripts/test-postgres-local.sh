#!/usr/bin/env bash
# Own a fresh synthetic cluster; never accepts a production DSN.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${PG_BIN:?Point PG_BIN to official PostgreSQL 17 binaries}"
GO=${GO:-go}
PG_TEST_PORT=${PG_TEST_PORT:-55442}
PG_TEST_COUNT=${PG_TEST_COUNT:-1}
[[ "$PG_TEST_COUNT" =~ ^[0-9]+$ ]] && ((PG_TEST_COUNT >= 1 && PG_TEST_COUNT <= 20)) || { echo 'Invalid test count' >&2; exit 2; }
[[ "$PG_TEST_PORT" =~ ^[0-9]+$ ]] && ((PG_TEST_PORT >= 1024 && PG_TEST_PORT <= 65535)) || { echo 'Invalid test port' >&2; exit 2; }
work=$(mktemp -d "${TMPDIR:-/tmp}/fanbbs-postgres.XXXXXXXX")
started=false
cleanup() {
 if "$started" || "$PG_BIN/pg_ctl" -D "$work/data" status >/dev/null 2>&1; then
  if ! "$PG_BIN/pg_ctl" -D "$work/data" -m fast -w stop; then
   echo "Could not stop disposable cluster; data retained at $work" >&2
   return 1
  fi
 fi
 # This path is created by mktemp above and contains only this test's data.
 if [[ -f "$work/data/PG_VERSION" && ! -f "$work/data/postmaster.pid" ]]; then
  rm -rf -- "$work/data"
 fi
 echo "Disposable cluster stopped and data removed; logs retained at $work"
}
trap cleanup EXIT
"$PG_BIN/initdb" -D "$work/data" -U fanbbs_test --auth-local=trust --auth-host=trust --no-locale -E UTF8 > "$work/initdb.log" 2>&1
"$PG_BIN/pg_ctl" -D "$work/data" -l "$work/server.log" -o "-h 127.0.0.1 -p $PG_TEST_PORT -c unix_socket_directories='' -c shared_buffers=32MB -c max_connections=30" -w start
started=true
"$PG_BIN/createdb" -h 127.0.0.1 -p "$PG_TEST_PORT" -U fanbbs_test fanbbs_test
export FANBBS_TEST_POSTGRES_DSN="postgres://fanbbs_test@127.0.0.1:$PG_TEST_PORT/fanbbs_test?sslmode=disable"
"$PG_BIN/psql" "$FANBBS_TEST_POSTGRES_DSN" -Atc 'SELECT version();'
"$GO" test ./internal/platform -run Postgres -count="$PG_TEST_COUNT" -timeout=2m -v
