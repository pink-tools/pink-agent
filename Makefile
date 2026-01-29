VERSION := dev-$(shell date +%Y-%m-%d_%H:%M:%S)
INSTALL_DIR := ~/pink-tools/pink-agent

build:
	go build -ldflags="-X main.version=$(VERSION)" -o pink-agent ./cmd/pink-agent

install: build
	cp pink-agent $(INSTALL_DIR)/pink-agent
