#!/bin/bash
set -xeou pipefail

DOCKER_REGISTRY=${DOCKER_REGISTRY:-kubedb}

IMG_REGISTRY=wrouesnel
IMG=postgres_exporter
TAG=v0.4.6

docker pull "$IMG_REGISTRY/$IMG:$TAG"

docker tag "$IMG_REGISTRY/$IMG:$TAG" "$DOCKER_REGISTRY/$IMG:$TAG"
docker push "$DOCKER_REGISTRY/$IMG:$TAG"
