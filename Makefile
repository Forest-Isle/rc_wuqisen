.PHONY: format test test-race vet compose-up compose-down
format:
	gofmt -w cmd internal test
test:
	go test ./...
test-race:
	go test -race ./...
vet:
	go vet ./...
compose-up:
	docker compose up -d --build --wait
compose-down:
	docker compose down -v
