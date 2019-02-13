#!/bin/bash
set -xeou pipefail

DOCKER_REGISTRY=${DOCKER_REGISTRY:-kubedb}

IMG=postgres-tools
SUFFIX=v3
DB_VERSION=9.6
PATCH=9.6.7

TAG="$DB_VERSION-$SUFFIX"
BASE_TAG="$PATCH-$SUFFIX"


docker pull "$DOCKER_REGISTRY/$IMG:$BASE_TAG"

docker tag "$DOCKER_REGISTRY/$IMG:$BASE_TAG" "$DOCKER_REGISTRY/$IMG:$TAG"
docker push "$DOCKER_REGISTRY/$IMG:$TAG"
