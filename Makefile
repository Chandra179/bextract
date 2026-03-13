.PHONY: run build test clean tidy swag

## run: start the HTTP server with go run (no build step required)
run:
	go run ./cmd/server

## test: run all tests
test:
	go test ./...

## swag: regenerate Swagger docs from annotations
swag:
	swag init -g cmd/server/main.go --output docs

## tidy: clean up go.mod and go.sum
tidy:
	go mod tidy