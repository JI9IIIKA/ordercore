PROJECT ?= ordercore
.PHONY: setup up down test integration fmt vet build
setup:
	python3 scripts/setup.py
up:
	PROJECT=$(PROJECT) docker compose up --build -d app
down:
	docker compose down
fmt:
	gofmt -w .
vet:
	go vet ./...
build:
	go build ./...
test:
	go test ./...
integration:
	test -n "$(TEST_DATABASE_URL)"
	go test -count=1 -v ./tests
