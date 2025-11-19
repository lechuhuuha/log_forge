BIN_DIR := bin
BINARY := $(BIN_DIR)/server
VERSION ?= 2
COMPOSE ?= docker compose
PPROF_ADDR_V1 ?= :6062
PPROF_ADDR_V2 ?= :6063
PPROF_SECONDS ?= 30
PROFILES_DIR ?= profiles
PROFILE_UI_PORT_V1 ?= :8085
PROFILE_UI_PORT_V2 ?= :8086
PROFILE_UI_PORT_COMPARE ?= :8087
PROFILE_UI_PORT_HEAP_COMPARE ?= :8088
PROFILE_UI_PORT_GOROUTINE_COMPARE ?= :8089

.PHONY: build run run-v1 run-v2 capture-profile-v1 capture-profile-v2 profile-run-v1 profile-run-v2 profile-cpu-v1 profile-cpu-v2 profile-heap-v1 profile-heap-v2 profile-goroutine-v1 profile-goroutine-v2 profile-ui-cpu-v1 profile-ui-cpu-v2 profile-ui-cpu-compare profile-ui-heap-compare profile-ui-goroutine-compare test loadtest loadtest-v1 loadtest-v2 stack-up stack-down infra-up infra-down kafka-topic

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BINARY) ./cmd

run: build
	./$(BINARY) -version=$(VERSION)

run-v1: build
	./$(BINARY) -version=1

run-v2: build
	./$(BINARY) -config=configs/config.v2.local.yaml

capture-profile-v1: build
	PROFILE_CAPTURE=1 PROFILE_NAME=v1 PROFILE_DIR=$(PROFILES_DIR) ./$(BINARY) -config=configs/config.v1.local.yaml

capture-profile-v2: build
	PROFILE_CAPTURE=1 PROFILE_NAME=v2 PROFILE_DIR=$(PROFILES_DIR) ./$(BINARY) -config=configs/config.v2.local.yaml

profile-run-v1: build
	PROFILE_ENABLED=1 PROFILE_ADDR=$(PPROF_ADDR_V1) ./$(BINARY) -version=1 -config=configs/config.v1.local.yaml

profile-run-v2: build
	PROFILE_ENABLED=1 PROFILE_ADDR=$(PPROF_ADDR_V2) ./$(BINARY) -config=configs/config.v2.local.yaml

profile-cpu-v1:
	@mkdir -p $(PROFILES_DIR)
	@ts=$$(date +%Y%m%d%H%M%S); \
	file=$(PROFILES_DIR)/v1_cpu_$${ts}.pprof; \
	echo "Saving CPU profile to $$file"; \
	curl -sS "http://localhost$(PPROF_ADDR_V1)/debug/pprof/profile?seconds=$(PPROF_SECONDS)" -o $$file; \
	go tool pprof -top $$file

profile-cpu-v2:
	@mkdir -p $(PROFILES_DIR)
	@ts=$$(date +%Y%m%d%H%M%S); \
	file=$(PROFILES_DIR)/v2_cpu_$${ts}.pprof; \
	echo "Saving CPU profile to $$file"; \
	curl -sS "http://localhost$(PPROF_ADDR_V2)/debug/pprof/profile?seconds=$(PPROF_SECONDS)" -o $$file; \
	go tool pprof -top $$file

profile-heap-v1:
	@mkdir -p $(PROFILES_DIR)
	@ts=$$(date +%Y%m%d%H%M%S); \
	file=$(PROFILES_DIR)/v1_heap_$${ts}.pprof; \
	echo "Saving heap profile to $$file"; \
	curl -sS "http://localhost$(PPROF_ADDR_V1)/debug/pprof/heap" -o $$file; \
	go tool pprof -top -alloc_space $$file

profile-heap-v2:
	@mkdir -p $(PROFILES_DIR)
	@ts=$$(date +%Y%m%d%H%M%S); \
	file=$(PROFILES_DIR)/v2_heap_$${ts}.pprof; \
	echo "Saving heap profile to $$file"; \
	curl -sS "http://localhost$(PPROF_ADDR_V2)/debug/pprof/heap" -o $$file; \
	go tool pprof -top -alloc_space $$file

profile-goroutine-v1:
	@mkdir -p $(PROFILES_DIR)
	@ts=$$(date +%Y%m%d%H%M%S); \
	file=$(PROFILES_DIR)/v1_goroutine_$${ts}.pprof; \
	echo "Saving goroutine profile to $$file"; \
	curl -sS "http://localhost$(PPROF_ADDR_V1)/debug/pprof/goroutine" -o $$file; \
	go tool pprof -top $$file

profile-goroutine-v2:
	@mkdir -p $(PROFILES_DIR)
	@ts=$$(date +%Y%m%d%H%M%S); \
	file=$(PROFILES_DIR)/v2_goroutine_$${ts}.pprof; \
	echo "Saving goroutine profile to $$file"; \
	curl -sS "http://localhost$(PPROF_ADDR_V2)/debug/pprof/goroutine" -o $$file; \
	go tool pprof -top $$file

profile-ui-cpu-v1:
	@file=$$(find $(PROFILES_DIR) -maxdepth 1 -type f \( -name "v1_cpu_*.pprof" -o -name "cpu_v1.prof" -o -name "v1_cpu_*.prof" \) 2>/dev/null | sort -r | head -n1); \
	if [ -z "$$file" ]; then echo "No v1 CPU profiles found. Run make profile-cpu-v1 or capture-profile-v1 first."; exit 1; fi; \
	echo "Serving $$file on http://localhost$(PROFILE_UI_PORT_V1)"; \
	go tool pprof -http=$(PROFILE_UI_PORT_V1) $$file

profile-ui-cpu-v2:
	@file=$$(find $(PROFILES_DIR) -maxdepth 1 -type f \( -name "v2_cpu_*.pprof" -o -name "cpu_v2.prof" -o -name "v2_cpu_*.prof" \) 2>/dev/null | sort -r | head -n1); \
	if [ -z "$$file" ]; then echo "No v2 CPU profiles found. Run make profile-cpu-v2 or capture-profile-v2 first."; exit 1; fi; \
	echo "Serving $$file on http://localhost$(PROFILE_UI_PORT_V2)"; \
	go tool pprof -http=$(PROFILE_UI_PORT_V2) $$file

profile-ui-cpu-compare:
	@file1=$$(find $(PROFILES_DIR) -maxdepth 1 -type f \( -name "v1_cpu_*.pprof" -o -name "cpu_v1.prof" -o -name "v1_cpu_*.prof" \) 2>/dev/null | sort -r | head -n1); \
	file2=$$(find $(PROFILES_DIR) -maxdepth 1 -type f \( -name "v2_cpu_*.pprof" -o -name "cpu_v2.prof" -o -name "v2_cpu_*.prof" \) 2>/dev/null | sort -r | head -n1); \
	if [ -z "$$file1" ] || [ -z "$$file2" ]; then echo "Missing CPU profiles. Run make profile-cpu-v1/v2 or capture-profile-v1/v2 first."; exit 1; fi; \
	echo "Comparing $$file1 vs $$file2 on http://localhost$(PROFILE_UI_PORT_COMPARE)"; \
	go tool pprof -http=$(PROFILE_UI_PORT_COMPARE) $$file1 $$file2

profile-ui-heap-compare:
	@file1=$$(find $(PROFILES_DIR) -maxdepth 1 -type f \( -name "v1_heap_*.pprof" -o -name "heap_v1.prof" -o -name "v1_heap_*.prof" \) 2>/dev/null | sort -r | head -n1); \
	file2=$$(find $(PROFILES_DIR) -maxdepth 1 -type f \( -name "v2_heap_*.pprof" -o -name "heap_v2.prof" -o -name "v2_heap_*.prof" \) 2>/dev/null | sort -r | head -n1); \
	if [ -z "$$file1" ] || [ -z "$$file2" ]; then echo "Missing heap profiles. Run make profile-heap-v1/v2 or capture-profile-v1/v2 first."; exit 1; fi; \
	echo "Comparing $$file1 vs $$file2 on http://localhost$(PROFILE_UI_PORT_HEAP_COMPARE)"; \
	go tool pprof -http=$(PROFILE_UI_PORT_HEAP_COMPARE) $$file1 $$file2

profile-ui-goroutine-compare:
	@file1=$$(find $(PROFILES_DIR) -maxdepth 1 -type f \( -name "v1_goroutine_*.pprof" -o -name "goroutine_v1.prof" -o -name "v1_goroutine_*.prof" \) 2>/dev/null | sort -r | head -n1); \
	file2=$$(find $(PROFILES_DIR) -maxdepth 1 -type f \( -name "v2_goroutine_*.pprof" -o -name "goroutine_v2.prof" -o -name "v2_goroutine_*.prof" \) 2>/dev/null | sort -r | head -n1); \
	if [ -z "$$file1" ] || [ -z "$$file2" ]; then echo "Missing goroutine profiles. Run make profile-goroutine-v1/v2 or capture-profile-v1/v2 first."; exit 1; fi; \
	echo "Comparing $$file1 vs $$file2 on http://localhost$(PROFILE_UI_PORT_GOROUTINE_COMPARE)"; \
	go tool pprof -http=$(PROFILE_UI_PORT_GOROUTINE_COMPARE) $$file1 $$file2

test:
	go test ./...

loadtest: loadtest-v1

loadtest-v1:
	@if command -v hey >/dev/null 2>&1; then \
		echo "running load test (version 1) with hey"; \
		hey -n 1000000 -c 50 -m POST -T "application/json" -d "`cat testdata/sample_logs.json`" http://localhost:8082/logs; \
	else \
		echo "hey not installed; install hey (https://github.com/rakyll/hey) to run load tests"; \
	fi

loadtest-v2:
	@if command -v hey >/dev/null 2>&1; then \
		echo "running load test (version 2) with hey"; \
		hey -n 1000000 -c 50 -m POST -T "application/json" -d "`cat testdata/sample_logs.json`" http://localhost:8083/logs; \
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
	@$(COMPOSE) exec kafka kafka-topics --delete --topic logs --bootstrap-server kafka:9092 >/dev/null 2>&1 || true
	@$(COMPOSE) exec kafka kafka-topics --create --if-not-exists --topic logs --partitions 10 --replication-factor 1 --bootstrap-server kafka:9092 >/dev/null 2>&1 || true
	@$(COMPOSE) exec kafka kafka-topics --alter --topic logs --partitions 10 --bootstrap-server kafka:9092 >/dev/null 2>&1 || true
