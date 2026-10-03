#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 sqlite SOURCE_DB WORK_DIR | postgres SOURCE_DSN RESTORE_DSN WORK_DIR" >&2
  exit 2
}

driver=${1:-}
case "$driver" in
  sqlite)
    [ "$#" -eq 3 ] || usage
    source_db=$2
    work_dir=$3
    mkdir -p "$work_dir"
    backup="$work_dir/fanbbs.sqlite.backup"
    restored="$work_dir/fanbbs.sqlite.restored"
    sqlite3 "$source_db" ".backup '$backup'"
    cp "$backup" "$restored"
    [ "$(sqlite3 "$restored" 'PRAGMA integrity_check;')" = "ok" ]
    source_versions=$(sqlite3 "$source_db" 'SELECT COUNT(*) FROM schema_migrations;')
    restored_versions=$(sqlite3 "$restored" 'SELECT COUNT(*) FROM schema_migrations;')
    [ "$source_versions" = "$restored_versions" ]
    printf 'sqlite backup/restore verified: %s migrations\n' "$restored_versions"
    ;;
  postgres)
    [ "$#" -eq 4 ] || usage
    source_dsn=$2
    restore_dsn=$3
    work_dir=$4
    mkdir -p "$work_dir"
    backup="$work_dir/fanbbs.postgres.dump"
    pg_dump --format=custom --no-owner --no-acl --dbname="$source_dsn" --file="$backup"
    pg_restore --clean --if-exists --no-owner --no-acl --dbname="$restore_dsn" "$backup"
    source_versions=$(psql "$source_dsn" -XAtqc 'SELECT COUNT(*) FROM schema_migrations;')
    restored_versions=$(psql "$restore_dsn" -XAtqc 'SELECT COUNT(*) FROM schema_migrations;')
    [ "$source_versions" = "$restored_versions" ]
    restored_users=$(psql "$restore_dsn" -XAtqc 'SELECT COUNT(*) FROM users;')
    printf 'postgres backup/restore verified: %s migrations, %s synthetic users\n' "$restored_versions" "$restored_users"
    ;;
  *) usage ;;
esac
