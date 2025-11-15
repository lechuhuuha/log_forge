BINARY := bin/server
VERSION ?= 2
COMPOSE ?= docker compose

.PHONY: build run run-v1 run-v2 test loadtest loadtest-v1 loadtest-v2 stack-up stack-down infra-up infra-down kafka-topic

build:
	@mkdir -p bin
	go build -o $(BINARY) ./cmd/server

run: build
	./$(BINARY) -version=$(VERSION)

run-v1: build
	./$(BINARY) -version=1

run-v2: build
	./$(BINARY) -config=configs/config.v2.local.yaml

test:
	go test ./...

loadtest: loadtest-v1

loadtest-v1:
	@if command -v hey >/dev/null 2>&1; then \
		echo "running load test (version 1) with hey"; \
		hey -n 100000 -c 50 -m POST -T "application/json" -d "`cat testdata/sample_logs.json`" http://localhost:8080/logs; \
	else \
		echo "hey not installed; install hey (https://github.com/rakyll/hey) to run load tests"; \
	fi

loadtest-v2:
	@if command -v hey >/dev/null 2>&1; then \
		echo "running load test (version 2) with hey"; \
		hey -n 100000 -c 50 -m POST -T "application/json" -d "`cat testdata/sample_logs.json`" http://localhost:8083/logs; \
	else \
		echo "hey not installed; install hey (https://github.com/rakyll/hey) to run load tests"; \
	fi

stack-up:
	$(COMPOSE) up -d --build kafka prometheus app-v1 app-v2
	@echo "Environment is up. Prometheus at http://localhost:9090, apps at http://localhost:8082 and http://localhost:8083"
	$(MAKE) kafka-topic

stack-down:
	$(COMPOSE) down

infra-up:
	$(COMPOSE) up -d kafka prometheus
	@echo "Kafka available at localhost:19092 (PLAINTEXT) and Prometheus at http://localhost:9090"
	$(MAKE) kafka-topic

infra-down:
	$(COMPOSE) stop kafka prometheus

kafka-topic:
	@echo "Ensuring 'logs' topic exists"
	@$(COMPOSE) exec kafka kafka-topics --create --if-not-exists --topic logs --partitions 3 --replication-factor 1 --bootstrap-server localhost:19092 >/dev/null 2>&1 || true
