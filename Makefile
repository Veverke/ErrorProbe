.PHONY: setup-hooks test test-coverage

## setup-hooks: configure git to use .githooks/ (run once per clone)
setup-hooks:
	git config core.hooksPath .githooks
	@chmod +x .githooks/pre-push 2>/dev/null || true
	@echo "Git hooks installed — pre-push will run tests and coverage gate."

## test: run unit tests only (fast, no coverage output)
test:
	go test -count=1 ./internal/... ./cmd/...

## test-coverage: run unit tests and print per-package coverage summary
test-coverage:
	go test -coverprofile=coverage.out -covermode=atomic -count=1 ./internal/... ./cmd/...
	go tool cover -func=coverage.out | tail -5
	go run ./scripts/check_coverage.go coverage.out 90
