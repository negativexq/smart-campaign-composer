COMPOSE_TEST := docker compose -f compose.test.yaml

.PHONY: test test-go test-frontend test-local test-e2e test-chaos benchmark test-env-up test-env-down up down verify-up verify-load verify-security verify-mutation verify-down

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

## test-chaos: kill and stop services under a running stack and check nothing is lost (takes a few minutes)
test-chaos:
	go test -tags=chaos -count=1 -timeout=15m -v ./tests/chaos/...

## benchmark: score a provider on the hard cases (mock unless LLM_PROVIDER is set; a real one costs money)
benchmark: test-env-up
	go test -tags=benchmark -count=1 -timeout=90m -run TestBenchmark -v ./tests/benchmark/

## verify-up: start the verification stack (own project, image and ports; seeds 116,000 users)
VERIFY_PROJECT ?= scc-verify
VERIFY_COMPOSE := COMPOSE_PROJECT_NAME=$(VERIFY_PROJECT) docker compose -f compose.yaml -f tests/verify/compose.verify.yaml
VERIFY_TEST := COMPOSE_PROJECT_NAME=$(VERIFY_PROJECT) go test -tags=verify -count=1 -v

verify-up:
	$(VERIFY_COMPOSE) up --build -d --wait

## verify-load: send full campaigns under faults; checks limits, retries and idempotency (about an hour)
verify-load:
	$(VERIFY_TEST) -timeout=4h ./tests/verify/load/

## verify-security: probe the verification stack from outside
verify-security:
	$(VERIFY_TEST) -timeout=30m ./tests/verify/security/

## verify-mutation: break the code on purpose and check the tests notice (needs test-env-up)
verify-mutation: test-env-up
	go test -tags=verify -count=1 -v -timeout=2h ./tests/verify/mutation/

## verify-down: remove the verification stack and its volumes
verify-down:
	$(VERIFY_COMPOSE) down -v

test-env-up:
	$(COMPOSE_TEST) up -d --wait postgres redis

test-env-down:
	$(COMPOSE_TEST) --profile runner down -v

## up: build and start the full system
up:
	docker compose up --build -d --wait

down:
	docker compose down
