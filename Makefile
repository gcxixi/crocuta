SHELL := /bin/sh

DOCKER ?= docker
COMPOSE ?= $(DOCKER) compose
SENTRYX_UI_PORT ?= 33000
SENTRYX_POSTGRES_PORT ?= 15432
SENTRYX_SERVER_PORT ?= 18080
POSTGRES_USER ?= sentryx
POSTGRES_DB ?= sentryx
TAIL ?= 120
SERVICE ?=

export SENTRYX_UI_PORT
export SENTRYX_POSTGRES_PORT
export SENTRYX_SERVER_PORT

.PHONY: help up restart rebuild ui-rebuild ui-restart stop down ps logs config \
	wait-postgres wait-api db-migrate db-shell health test test-ui

help:
	@printf '%s\n' \
		'Local SentryX development targets:' \
		'  make up                 Start the stack, apply migrations, wait for API' \
		'  make restart            Stop and start in dependency order' \
		'  make rebuild            Rebuild every image, then restart the stack' \
		'  make ui-rebuild         Rebuild and replace only the UI container' \
		'  make ui-restart         Restart only the existing UI container' \
		'  make ps                 Show Compose service status' \
		'  make logs               Follow all logs (SERVICE=server for one service)' \
		'  make health             Check Server and UI proxy APIs' \
		'  make db-migrate         Apply all SQL migrations in filename order' \
		'  make db-shell           Open psql in the PostgreSQL container' \
		'  make stop               Stop containers without removing them' \
		'  make down               Remove containers/network, preserve named volumes' \
		'  make test               Run Go tests and the UI production build'

up:
	$(COMPOSE) up -d postgres
	$(MAKE) wait-postgres
	$(MAKE) db-migrate
	$(COMPOSE) up -d
	$(MAKE) wait-api
	$(COMPOSE) ps

restart:
	$(COMPOSE) stop
	$(MAKE) up

rebuild:
	$(COMPOSE) build
	$(MAKE) restart

ui-rebuild:
	$(COMPOSE) build ui
	$(COMPOSE) up -d --no-deps --force-recreate ui
	$(MAKE) wait-api

ui-restart:
	$(COMPOSE) restart ui
	$(MAKE) wait-api

stop:
	$(COMPOSE) stop

down:
	$(COMPOSE) down

ps:
	$(COMPOSE) ps -a

logs:
	$(COMPOSE) logs -f --tail=$(TAIL) $(SERVICE)

config:
	$(COMPOSE) config

wait-postgres:
	@attempt=0; \
	until $(COMPOSE) exec -T postgres pg_isready -U $(POSTGRES_USER) -d $(POSTGRES_DB) >/dev/null 2>&1; do \
		attempt=$$((attempt + 1)); \
		if [ $$attempt -ge 30 ]; then echo 'PostgreSQL did not become ready' >&2; exit 1; fi; \
		sleep 1; \
	done

wait-api:
	@attempt=0; \
	until curl -fsS http://127.0.0.1:$(SENTRYX_SERVER_PORT)/api/0/organizations/default/projects/ >/dev/null \
		&& curl -fsS http://127.0.0.1:$(SENTRYX_UI_PORT)/api/0/organizations/default/projects/ >/dev/null; do \
		attempt=$$((attempt + 1)); \
		if [ $$attempt -ge 30 ]; then echo 'SentryX API did not become ready' >&2; exit 1; fi; \
		sleep 1; \
	done

db-migrate: wait-postgres
	@set -e; \
	for migration in migrations/*.sql; do \
		echo "Applying $$migration"; \
		$(COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -U $(POSTGRES_USER) -d $(POSTGRES_DB) < "$$migration" >/dev/null; \
	done

db-shell:
	$(COMPOSE) exec postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)

health:
	@curl -fsS http://127.0.0.1:$(SENTRYX_SERVER_PORT)/api/0/organizations/default/projects/ >/dev/null
	@curl -fsS http://127.0.0.1:$(SENTRYX_UI_PORT)/api/0/organizations/default/projects/ >/dev/null
	@printf 'Server API: OK (%s)\nUI proxy:  OK (%s)\n' \
		'http://127.0.0.1:$(SENTRYX_SERVER_PORT)' \
		'http://127.0.0.1:$(SENTRYX_UI_PORT)'

test:
	go test ./...
	$(MAKE) test-ui

test-ui:
	npm run build --prefix ui
