.PHONY: build run clean

build:
	go build -o bin/twitch-bot ./cmd

run: build
	./bin/twitch-bot \
		-channel="your_channel" \
		-username="your_bot_username" \
		-token="your_oauth_token" \
		-spotify-id="your_spotify_id" \
		-spotify-secret="your_spotify_secret"

dev:
	go run ./cmd/main.go \
		-channel="your_channel" \
		-username="your_bot_username" \
		-token="your_oauth_token" \
		-spotify-id="your_spotify_id" \
		-spotify-secret="your_spotify_secret"

clean:
	rm -rf bin/
	rm -f bot.db

deps:
	go mod download
	go mod tidy

test:
	go test ./...
