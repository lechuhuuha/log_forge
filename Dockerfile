# syntax=docker/dockerfile:1

FROM golang:1.21-alpine AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server ./cmd/server

FROM alpine:3.18
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/server /app/server
EXPOSE 8080
CMD ["/app/server", "-version=1"]
