.PHONY: dev-api dev-web generate fmt build check

dev-api:
	go run ./cmd/mobius

dev-web:
	cd web && bun run dev

generate:
	go tool sqlc generate
	go tool gork openapi generate --build ./cmd/mobius --source ./internal/api --output api/openapi.json
	cd web && bun run gen:api

fmt:
	golangci-lint fmt
	cd web && bun run format

build:
	cd web && bun run build
	go build -o bin/mobius ./cmd/mobius

check:
	.mobius/check
