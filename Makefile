.PHONY: dev-api dev-web generate build check

dev-api:
	go run ./cmd/mobius

dev-web:
	cd web && pnpm dev

generate:
	go tool gork openapi generate --build ./cmd/mobius --source ./internal/api --output api/openapi.json
	cd web && pnpm gen:api

build:
	cd web && pnpm build
	go build -o bin/mobius ./cmd/mobius

check:
	.mobius/check
