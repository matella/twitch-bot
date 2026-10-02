SHELL := /bin/bash

.PHONY: build test vet tidy run

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/twitch-bot ./cmd/twitch-bot

test:
	go test ./...

vet:
	go vet ./...

# Résout les dépendances et écrit go.mod / go.sum (à commiter).
tidy:
	go mod tidy

# Lance le bot en local avec la configuration de deploy/.env
run: build
	@set -a && source deploy/.env && set +a && ./bin/twitch-bot
