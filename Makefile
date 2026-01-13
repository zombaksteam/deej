CURRENT_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

.PHONY: default
default: build-windows

build-windows: build-windows-debug build-windows-gui

build-windows-debug:
	go mod tidy
	go mod vendor
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -mod vendor -a -o ./deej-debug-amd64.exe -ldflags='-extldflags "-static"' \
		${CURRENT_DIR}/pkg/deej/cmd

build-windows-gui:
	go mod tidy
	go mod vendor
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -mod vendor -a -o ./deej-amd64.exe -ldflags='-H=windowsgui -s -w -extldflags "-static"' \
		${CURRENT_DIR}/pkg/deej/cmd
