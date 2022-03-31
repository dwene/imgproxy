#!/bin/bash

set -e

GO_VERSION=$1
VIPS_VERSION=$2

curl -Ls https://go.dev/dl/go$GO_VERSION.linux-amd64.tar.gz \
  | tar -xzC /usr/local

export GO111MODULE=on
export GOPATH=/usr/local/go
export PATH="$PATH:$GOPATH/bin"
export CGO_LDFLAGS_ALLOW="-s|-w"

export LD_LIBRARY_PATH="$LD_LIBRARY_PATH:/root/vips/$VIPS_VERSION/lib"
export PKG_CONFIG_PATH="$PKG_CONFIG_PATH:/root/vips/$VIPS_VERSION/lib/pkgconfig"

curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh | BINARY=golangci-lint sh -s -- -b $(go env GOPATH)/bin v1.18.0

golangci-lint run
