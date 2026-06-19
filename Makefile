.PHONY: proto setup

# Default target
all: proto

# Trigger gRPC code generation from the Schema Registry
proto:
	@echo "Invoking Schema Registry Generator..."
	@bash ../nacl-proto-schema/generate.sh

# Developer setup target to install Go protoc plugins
setup:
	@echo "Installing Go protobuf and gRPC plugins..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "Setup complete. Make sure Go bin directory ($$(go env GOPATH)/bin) is in your PATH."
