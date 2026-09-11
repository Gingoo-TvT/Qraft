.DEFAULT_GOAL := help
COMPOSE := docker compose
MODEL_VERSION_ID ?=
.PHONY: help setup test build desktop backend-up worker-up stop
help:
	@echo "Qraft development"
	@echo "  make setup       Create local credentials without replacing existing settings"
	@echo "  make test        Test all Go modules and build both interfaces"
	@echo "  make build       Build server images"
	@echo "  make desktop     Build the bundled Windows interface"
	@echo "  make backend-up  Start a local configuration workspace and sandbox"
	@echo "  make worker-up   Apply saved embedding settings and start generation"
	@echo "  make stop        Stop this Compose project, preserving volumes"
setup:
	python3 scripts/configure.py
test:
	python3 -m unittest discover -s scripts -p "*_test.py"
	go -C backend test ./... -count=1
	go -C desktop test ./... -count=1
	go -C sandbox test ./... -count=1
	cd frontend && npm ci && npm run lint && npm run build && npm run build:desktop
	python3 scripts/check-repository.py
build: setup
	python3 scripts/configure.py --refresh-revision
	$(COMPOSE) build
desktop:
	cd frontend && npm ci && npm run build:desktop
	cd desktop && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -mod=vendor -o /tmp/Qraft-dev.exe ./cmd/qraft
backend-up: build
	python3 scripts/configure.py --bind-sandbox
	$(COMPOSE) up -d --wait --wait-timeout 360 caddy sandbox
worker-up:
	python3 scripts/configure.py --saved-embedding http://localhost:$$(sed -n 's/^HTTP_PORT=//p' .env) --model-version-id "$(MODEL_VERSION_ID)"
	$(COMPOSE) up -d --wait --wait-timeout 360 api worker
stop:
	$(COMPOSE) stop
