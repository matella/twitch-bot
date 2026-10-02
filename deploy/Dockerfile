# Contexte de build : la racine du dépôt (voir deploy/docker-compose.yml).
# GOTOOLCHAIN=auto : si une dépendance exige un Go plus récent que l'image, Go télécharge
# le bon compilateur au lieu d'échouer (« requires go >= 1.xx »).
FROM golang:1.26-alpine AS build
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY . .
# Tant que go.sum n'est pas commité, « go mod tidy » résout les dépendances.
# Une fois go.mod / go.sum complets dans le dépôt (la CI les y écrit), cette commande ne change plus rien.
RUN go mod tidy \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/twitch-bot ./cmd/twitch-bot

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 app \
 && mkdir /data && chown app:app /data
COPY --from=build /out/twitch-bot /usr/local/bin/twitch-bot
USER app
ENV PORT=9090 \
    DB_PATH=/data/bot.db
VOLUME /data
EXPOSE 9090
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["twitch-bot", "healthcheck"]
ENTRYPOINT ["twitch-bot"]
