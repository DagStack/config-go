.PHONY: help test test-cov vet lint fmt tidy conformance clean

help:
	@echo "dagstack-config-go — Go binding for dagstack/config-spec"
	@echo ""
	@echo "Targets:"
	@echo "  test          go test ./..."
	@echo "  test-cov      go test with coverage report"
	@echo "  vet           go vet ./..."
	@echo "  lint          golangci-lint run"
	@echo "  fmt           gofmt -s -w ."
	@echo "  tidy          go mod tidy"
	@echo "  conformance   go test -run TestConformance (graceful skip when spec/ submodule is absent)"
	@echo "  clean         rm -rf coverage.out coverage.html"

test:
	go test -race ./...

test-cov:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

conformance:
	go test -race -run TestConformance -v ./...

clean:
	rm -f coverage.out coverage.html
