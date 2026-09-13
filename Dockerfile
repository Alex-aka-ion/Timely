# syntax=docker/dockerfile:1

# --- build --------------------------------------------------------------
# CGO_ENABLED=1 обязателен: драйвер БД (github.com/mattn/go-sqlite3) — это
# cgo-обвязка над самим SQLite, а не чистый Go. Официальный образ golang на
# Debian уже содержит gcc/libc6-dev "из коробки" (иначе бы cgo не работал
# ни для одного пакета) — отдельно ставить компилятор не нужно.
FROM golang:1.22-bookworm AS build
WORKDIR /src

# Сначала только go.mod/go.sum — слой с зависимостями кэшируется отдельно
# от исходников и не пересобирается при каждом изменении .go-файла.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /out/booking-bot ./cmd

# --- runtime --------------------------------------------------------------
# Debian-slim, а не Alpine: бинарник собран с CGO на glibc (Debian Bookworm)
# — на Alpine (musl) он не запустится ("exec format error"/segfault при
# первом обращении к sqlite3), поэтому база рантайма обязана совпадать по
# libc с образом сборки.
FROM debian:bookworm-slim

# ca-certificates — без них TLS-запросы к api.telegram.org и
# www.googleapis.com не пройдут проверку сертификата.
# tzdata — база часовых поясов для TZ (см. docker-compose.yml/.env):
# планировщик форматирует время занятий через time.Local, без tzdata любой
# TZ, кроме UTC, был бы недоступен.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
    && rm -rf /var/lib/apt/lists/*

# Системный пользователь без shell и домашней директории — тот же принцип,
# что и в deploy/booking-bot.service для systemd-варианта деплоя.
# Фиксированный UID/GID (а не "какой достанется" от --system) — чтобы
# заранее и предсказуемо chown'ить на хосте каталог ./data под bind mount
# (см. README): docker-compose создаёт /app/data из хостового ./data,
# владелец которого — хостовый root, если каталог не подготовить заранее,
# а не то, что RUN chown ниже проставил внутри образа для пустого /app/data.
RUN groupadd --system --gid 10001 booking-bot \
    && useradd --system --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin booking-bot

WORKDIR /app
COPY --from=build /out/booking-bot ./booking-bot
RUN chown -R booking-bot:booking-bot /app
USER booking-bot

# Ни один порт не публикуется: бот сам инициирует все соединения
# (Telegram long-polling, периодические запросы к Google Calendar) и не
# принимает входящих HTTP-запросов.
ENTRYPOINT ["./booking-bot"]
