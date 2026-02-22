# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=none
ARG BUILD_DATE=unknown
ARG LDFLAGS=""
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X main.Version=${BUILD_VERSION} -X main.Commit=${BUILD_COMMIT} -X main.BuildDate=${BUILD_DATE} ${LDFLAGS}" \
    -o /app/server ./cmd

FROM alpine:3.18
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/server /app/server
COPY config/examples /config
EXPOSE 8082
CMD ["/app/server", "-config=/config/config.v1.yaml"]
