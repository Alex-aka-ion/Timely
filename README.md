# Booking Bot

Telegram-бот, который рассылает родителям учеников напоминания о занятиях по расписанию из Google Calendar. Преподаватель управляет учениками и привязкой событий через inline-кнопки в боте.

Реализован по плану `booking-bot-final-plan.docx`. Один Go-бинарник: бот + планировщик в одной горутине каждый.

## Возможности

- Регистрация родителей через `/start` — никаких других приложений не нужно.
- Преподаватель управляет учениками и событиями прямо в боте.
- Поддерживаются повторяющиеся события Google Calendar.
- К одному ученику можно привязать несколько контактов (оба родителя).
- Настраиваемые интервалы напоминаний — глобально и на уровне ученика.
- Архитектура за интерфейсами: `Store`, `notify.Sender`, `calendar.Client`, `admin.UI`. Можно добавить WhatsApp/Viber/Web-интерфейс без переписывания бизнес-логики.

## Структура проекта

```
booking-bot/
├── cmd/                    # точка входа, graceful shutdown
├── internal/
│   ├── config/             # читает .env, валидирует
│   ├── logger/             # slog + tint, WithContext/FromContext
│   ├── store/              # интерфейс Store + SQLite + миграции
│   ├── notify/             # Dispatcher + интерфейс Sender
│   ├── admin/              # интерфейс UI для уведомлений преподавателю
│   ├── bot/                # Telegram-бот: handler_parent, handler_teacher, FSM
│   ├── calendar/           # клиент Google Calendar
│   └── scheduler/          # планировщик напоминаний
├── deploy/                 # systemd-юнит
├── scripts/                # install.sh, backup.sh
├── Makefile
└── README.md
```

## Настройка перед первым запуском

### 1. Telegram-бот

1. Откройте Telegram, найдите [@BotFather](https://t.me/BotFather).
2. `/newbot` → отображаемое имя → username (должен заканчиваться на `bot`).
3. Сохраните полученный токен как `BOT_TOKEN` в `.env`.
4. Узнайте свой telegram_id через [@userinfobot](https://t.me/userinfobot) → сохраните как `TEACHER_TELEGRAM_ID`.

### 2. Google Calendar API

1. https://console.cloud.google.com → создать проект.
2. APIs & Services → Library → включить **Google Calendar API**.
3. Credentials → Create Credentials → OAuth 2.0 Client ID → тип **Desktop App**.
4. Скачать `credentials.json` в корень проекта.
5. OAuth consent screen → добавить свой Google-аккаунт в Test users.
6. Первичная авторизация:
   ```bash
   ./booking-bot --auth
   ```
   Перейдите по ссылке, разрешите доступ, вставьте код в терминал. `token.json` сохранится автоматически.

### 3. .env

Скопируйте `.env.example` в `.env` и заполните значения. Проверьте, что `.env` в `.gitignore`.

```bash
cp .env.example .env
chmod 600 .env credentials.json token.json
```

## Локальная разработка

Требуется Go 1.22+.

```bash
make tidy        # подтянуть зависимости
make test        # запустить все тесты с -race
make cover       # отчёт покрытия в coverage.html
make vet         # go vet
make vuln        # govulncheck (нужен go install golang.org/x/vuln/cmd/govulncheck@latest)
make mocks       # сгенерировать моки (нужен go install github.com/vektra/mockery/v2@latest)
make build       # собрать бинарник
make build-linux # собрать для Linux/amd64 (для деплоя)
```

## Команды бота

### Для родителей

| Команда | Описание |
|---------|----------|
| `/start` | Регистрация: запрашивает имя, сохраняет, уведомляет преподавателя |

### Для преподавателя

| Команда | Описание |
|---------|----------|
| `/students` | Список учеников: контакты, события, настройки |
| `/students_new` | Создать ученика вручную |
| `/unlinked` | Зарегистрированные но не привязанные родители |
| `/events` | Мастер-события Calendar без ученика — для привязки |
| `/settings` | Изменение глобальных интервалов напоминаний |
| `/cancel` | Отменить текущий диалог |

## Деплой на VPS

Любой Linux VPS (Ubuntu 22.04+), минимум 512 MB RAM. Go на сервере не нужен.

```bash
# 1. Локально: собрать бинарник для Linux
make build-linux

# 2. Скопировать на сервер
scp booking-bot user@server:/tmp/
scp .env credentials.json token.json user@server:/tmp/
scp -r deploy scripts user@server:/tmp/

# 3. На сервере (root):
sudo bash /tmp/scripts/install.sh
sudo cp /tmp/booking-bot /opt/booking-bot/
sudo cp /tmp/.env /tmp/credentials.json /tmp/token.json /opt/booking-bot/
sudo chown booking-bot:booking-bot /opt/booking-bot/{booking-bot,.env,credentials.json,token.json}
sudo chmod 600 /opt/booking-bot/{.env,credentials.json,token.json}
sudo chmod 750 /opt/booking-bot/booking-bot

# 4. Запуск
sudo systemctl enable --now booking-bot
sudo journalctl -u booking-bot -f
```

### Бэкапы

```bash
sudo cp /tmp/scripts/backup.sh /opt/booking-bot/scripts/
sudo chmod 750 /opt/booking-bot/scripts/backup.sh

# В crontab root:
0 3 * * * /opt/booking-bot/scripts/backup.sh >> /var/log/booking-backup.log 2>&1
```

Скрипт использует `sqlite3 .backup` (безопасно при WAL-режиме), хранит бэкапы 30 дней (`RETAIN_DAYS`).

## Безопасность

- Секреты (`.env`, `credentials.json`, `token.json`) в `.gitignore`, на сервере `chmod 600`.
- Системный пользователь без shell: `useradd --system --no-create-home --shell /usr/sbin/nologin booking-bot`.
- systemd-юнит c `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, ограничением `RestrictAddressFamilies`.
- Минимальный OAuth scope: `calendar.events` (не полный календарь).
- Rate limiter: 5 сообщений/мин на пользователя.
- Роль преподавателя по `TEACHER_TELEGRAM_ID` из конфига — никакой БД для ролей.
- В логах **никогда** не пишутся `full_name` и `username` — только `user_id` и `messenger`.
- `PRAGMA foreign_keys=ON`, `journal_mode=WAL`.
- В CI рекомендуется gitleaks + govulncheck.

## Тестирование

| Пакет | Тип | Зависимости |
|-------|-----|-------------|
| `store/` | интеграционные | SQLite `:memory:` |
| `scheduler/` | юнит | моки `Store`, `CalendarClient`, `Sender` |
| `notify/` | юнит | моки `Sender`, `Store` |
| `bot/dialog`, `bot/ratelimit` | юнит | без зависимостей |
| `bot/handler_teacher` | юнит | in-memory store |
| `calendar/` | интеграционные | `httptest`-мок Google API |
| `config/`, `logger/` | юнит | `os.Setenv`, `bytes.Buffer` |

```bash
make test
make cover  # открыть coverage.html
```

## Расширение

- **Новый мессенджер**: реализовать `notify.Sender` (Messenger() + Send()), зарегистрировать в `Dispatcher`.
- **Web-UI для преподавателя**: реализовать `admin.UI` (NotifyNewUser, NotifyError) — никаких изменений в планировщике.
- **PostgreSQL вместо SQLite**: реализовать `store.Store` — миграции уже отделены.

## Лицензия

Частный проект.
