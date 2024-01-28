#!/bin/bash
set -e
TAG=eureka-exporter:v0.1
GOARCH="amd64" GOOS="linux" go build -o eureka-exporter cmd/eureka_exporter.go
docker build -t ${TAG} .
