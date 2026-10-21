# INTUITION — common tasks. Run from the repo root.
GO ?= go

.PHONY: dev dev-backend dev-frontend demo test test-backend test-frontend validate-content content-stats export-demo build e2e db-up docker

dev: ## Run Go server (:8080) and Vite (:5173) together
	@$(MAKE) -j2 dev-backend dev-frontend

dev-backend:
	cd backend && $(GO) run ./cmd/server

dev-frontend:
	cd frontend && npm run dev

demo: ## Frontend only, offline demo mode (no Go needed)
	cd frontend && npm run dev

validate-content: ## Fail on malformed / duplicate / placeholder questions
	cd backend && $(GO) run ./cmd/content validate -dir ../content

content-stats:
	cd backend && $(GO) run ./cmd/content stats -dir ../content

export-demo: ## Regenerate the offline demo pack after editing content
	cd backend && $(GO) run ./cmd/content export-demo -dir ../content -out ../frontend/src/demo/pack.json

test: validate-content test-backend test-frontend

test-backend:
	cd backend && $(GO) test -race ./...

test-frontend:
	cd frontend && npm run typecheck && npm test

e2e: ## Real server + 4 WebSocket bots play a full game
	./tests/e2e.sh

build:
	cd frontend && npm run build
	cd backend && CGO_ENABLED=0 $(GO) build -o ../bin/intuition-server ./cmd/server

db-up:
	docker compose up -d db

docker:
	docker build -t intuition .
