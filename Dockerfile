# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS build
WORKDIR /src
ARG TARGETOS
ARG TARGETARCH
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/api ./cmd/api && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/migrate ./cmd/migrate

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates && addgroup -S productor && adduser -S -G productor productor && mkdir -p /var/lib/productor/uploads && chown -R productor:productor /var/lib/productor
WORKDIR /app
COPY --from=build --chown=productor:productor /out/api /app/api
COPY --from=build --chown=productor:productor /out/migrate /app/migrate
USER productor
EXPOSE 8080
ENTRYPOINT ["/app/api"]
