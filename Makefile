.PHONY: run build test clean tidy swag docker-build docker-up docker-down

## docker-up: start the server in Docker (detached)
up:
	docker compose up --build -d

run:
	docker compose up -d

## test: run all tests
test:
	go test ./...

## swag: regenerate Swagger docs from annotations
swag:
	swag init -g cmd/server/main.go --output docs

## tidy: clean up go.mod and go.sum
tidy:
	go mod tidy