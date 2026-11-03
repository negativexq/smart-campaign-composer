COMPOSE_TEST := docker compose -f compose.test.yaml

.PHONY: test test-go test-frontend test-local test-e2e test-env-up test-env-down up down

## test: run all tests in containers (needs only Docker, no API key)
test: test-go test-frontend

test-go:
	$(COMPOSE_TEST) run --rm test-go

test-frontend:
	$(COMPOSE_TEST) run --rm test-frontend

## test-local: run Go tests with the local toolchain against the test containers
test-local: test-env-up
	go test -race -count=1 ./...

## test-e2e: run the acceptance test against a stack started with `make up`
test-e2e:
	go test -tags=e2e -count=1 ./tests/e2e/...

test-env-up:
	$(COMPOSE_TEST) up -d --wait postgres redis

test-env-down:
	$(COMPOSE_TEST) --profile runner down -v

## up: build and start the full system
up:
	docker compose up --build -d --wait

down:
	docker compose down
