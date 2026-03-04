#!/usr/bin/env bash

set -e

GREEN="\033[0;32m"
CYAN="\033[0;36m"
RESET="\033[0m"

cd "$(dirname "$0")/.."

echo -e 
if [ -x ~/go/bin/govulncheck ]; then
    echo -e "${CYAN}Running govulncheck...${RESET}"
    ~/go/bin/govulncheck ./...
else
    echo "Skipping govulncheck: not found" ; sleep 3
fi

echo -e "${CYAN}Running go vet...${RESET}"
go vet ./...

echo -e "${CYAN}Running go test...${RESET}"
go test -v -race ./...

if command -v golangci-lint &>/dev/null; then
    echo -e "${CYAN}Running golangci-lint...${RESET}"
    golangci-lint run ./...
else
    echo -e "${CYAN}Skipping golangci-lint (not installed)${RESET}"
    echo "  Install: https://golangci-lint.run/welcome/install/"
fi

echo -e "\n🎉 All checks ${GREEN}passed!${RESET}"
