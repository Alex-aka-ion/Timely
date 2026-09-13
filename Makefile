# Booking Bot — основные команды разработки и сборки.

BINARY      := booking-bot
GO          ?= go
GOOS_LINUX  := linux
GOARCH      := amd64
LDFLAGS     := -s -w

.PHONY: help
help:
	@echo "Цели:"
	@echo "  build       — собрать бинарник для текущей платформы"
	@echo "  build-linux — собрать для Linux/amd64 (для деплоя)"
	@echo "  test        — запустить все тесты"
	@echo "  cover       — отчёт покрытия HTML"
	@echo "  vet         — go vet"
	@echo "  vuln        — govulncheck (требует установки)"
	@echo "  mocks       — сгенерировать моки через mockery"
	@echo "  tidy        — go mod tidy"
	@echo "  clean       — удалить артефакты сборки"
	@echo "  docker-up   — собрать и запустить в Docker (docker compose up -d --build)"
	@echo "  docker-logs — логи контейнера"
	@echo "  docker-down — остановить контейнер"

.PHONY: build
build:
	$(GO) build -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd

.PHONY: build-linux
build-linux:
	GOOS=$(GOOS_LINUX) GOARCH=$(GOARCH) $(GO) build -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd

.PHONY: test
test:
	$(GO) test ./... -race -count=1

.PHONY: cover
cover:
	$(GO) test ./... -coverprofile=coverage.out
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Отчёт: coverage.html"

.PHONY: vet
vet:
	$(GO) vet ./...

# Установка: go install golang.org/x/vuln/cmd/govulncheck@latest
.PHONY: vuln
vuln:
	govulncheck ./...

# Установка: go install github.com/vektra/mockery/v2@latest
.PHONY: mocks
mocks:
	mockery --all --dir internal/ --output internal/mocks/

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: clean
clean:
	rm -f $(BINARY) coverage.out coverage.html

.PHONY: docker-up
docker-up:
	docker compose up -d --build

.PHONY: docker-logs
docker-logs:
	docker compose logs -f

.PHONY: docker-down
docker-down:
	docker compose down
