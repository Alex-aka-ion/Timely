#!/usr/bin/env bash
# Скрипт развёртывания на чистый Linux-сервер.
# Запускать с правами root.
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/booking-bot}"

# 1. Создаём системного пользователя без shell.
if ! id booking-bot >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin booking-bot
fi

# 2. Создаём директории.
mkdir -p "$INSTALL_DIR/data"
chown -R booking-bot:booking-bot "$INSTALL_DIR"
chmod 750 "$INSTALL_DIR"
chmod 700 "$INSTALL_DIR/data"

# 3. Устанавливаем systemd-юнит.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cp "$SCRIPT_DIR/../deploy/booking-bot.service" /etc/systemd/system/

# 4. Реалоадим systemd.
systemctl daemon-reload

echo "Готово."
echo "Скопируйте бинарник, .env, credentials.json, token.json в $INSTALL_DIR"
echo "и установите права: chmod 600 .env credentials.json token.json"
echo ""
echo "Затем: sudo systemctl enable --now booking-bot"
echo "Логи:  sudo journalctl -u booking-bot -f"
