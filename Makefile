BIN := sm
build:
	go build -o $(BIN) ./cmd/sm
test:
	go test ./...
lint:
	gofmt -l . && go vet ./...
.PHONY: build test lint
