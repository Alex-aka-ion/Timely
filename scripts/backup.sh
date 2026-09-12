#!/usr/bin/env bash
# Ежедневный бэкап SQLite-базы.
# Установка в cron: 0 3 * * * /opt/booking-bot/scripts/backup.sh
set -euo pipefail

DB_PATH="${DB_PATH:-/opt/booking-bot/data/booking.db}"
BACKUP_DIR="${BACKUP_DIR:-/backups}"
RETAIN_DAYS="${RETAIN_DAYS:-30}"

mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"

ts="$(date +%Y%m%d_%H%M%S)"
target="$BACKUP_DIR/booking-${ts}.db"

# .backup безопаснее чем cp — он использует API SQLite, корректно
# обрабатывая WAL-режим и работающее приложение.
sqlite3 "$DB_PATH" ".backup '$target'"
chmod 600 "$target"

# Чистка старых бэкапов.
find "$BACKUP_DIR" -name 'booking-*.db' -type f -mtime "+$RETAIN_DAYS" -delete

echo "Бэкап создан: $target"
