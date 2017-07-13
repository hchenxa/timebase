PHONY: build doc fmt lint run test vendor_clean vendor_get vendor_update vet

# Prepend our _vendor directory to the system GOPATH
# so that import path resolution will prioritize
# our third party snapshots.

default: build

build:
	go build -v -i -o bin/tbpolicy github.com/hchenxa/timebase/
