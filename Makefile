.PHONY: run build test clean tidy swag docker-build docker-up docker-down

## docker-build: build the Docker image
docker-build:
	docker compose build

## docker-up: start the server in Docker (detached)
docker-up:
	docker compose up -d

## docker-down: stop and remove Docker containers
docker-down:
	docker compose down

## test: run all tests
test:
	go test ./...

## swag: regenerate Swagger docs from annotations
swag:
	swag init -g cmd/server/main.go --output docs

## tidy: clean up go.mod and go.sum
tidy:
	go mod tidy