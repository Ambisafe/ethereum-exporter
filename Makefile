
DOCKER_IMAGE_NAME ?= ethereum-exporter

build:
	echo ">> building binaries..."
	sh -c ./scripts/build.sh

docker:
	echo ">> building docker image..."

.PHONY: build docker
