COMPOSE_TEST := docker compose -f compose.test.yaml

.PHONY: test test-go test-frontend test-local test-e2e test-chaos benchmark test-env-up test-env-down up down verify-up verify-load verify-security verify-mutation verify-down scan scan-secrets scan-images

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

## scan: look for secrets in the repository and known vulnerabilities in both images (needs only Docker)
TRIVY_VERSION := 0.73.0
# Trivy runs in its own container and reads its input from stdin, so it needs no Docker socket.
TRIVY := docker run --rm -i -v scc-trivy-cache:/root/.cache/trivy --entrypoint sh aquasec/trivy:$(TRIVY_VERSION) -c

scan: scan-secrets scan-images

## scan-secrets: scan the files git tracks or would track, so .env and other ignored files stay out
scan-secrets:
	git ls-files -z --cached --others --exclude-standard | tar --null -T - -cf - | \
		$(TRIVY) 'mkdir /src && tar -xf - -C /src && trivy fs --quiet --scanners secret --exit-code 1 /src'

## scan-images: build the backend and frontend images and fail on fixable HIGH or CRITICAL vulnerabilities
scan-images:
	docker build -q -t scc-scan-backend .
	docker build -q -t scc-scan-frontend frontend
	for image in scc-scan-backend scc-scan-frontend; do \
		echo "Scanning $$image"; \
		docker save $$image | $(TRIVY) \
			"cat > /tmp/image.tar && trivy image --quiet --input /tmp/image.tar --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1" || exit 1; \
	done
