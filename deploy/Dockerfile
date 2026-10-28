FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /agenttracer ./cmd/agenttracer

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/*
COPY --from=build /agenttracer /usr/local/bin/agenttracer
# Run as root so the host-mounted secrets/api_key (chmod 600) is readable.
# Harden with a dedicated uid + matching secret ownership in M2.
EXPOSE 1984
ENTRYPOINT ["/usr/local/bin/agenttracer"]
