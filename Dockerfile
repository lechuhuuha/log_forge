# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server ./cmd

FROM alpine:3.18
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/server /app/server
EXPOSE 8082
CMD ["/app/server", "-version=1"]
