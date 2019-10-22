#!/bin/bash
set -xeou pipefail

GOPATH=$(go env GOPATH)
REPO_ROOT=$GOPATH/src/kubedb.dev/postgres

source "$REPO_ROOT/hack/libbuild/common/lib.sh"
source "$REPO_ROOT/hack/libbuild/common/kubedb_image.sh"

DOCKER_REGISTRY=${DOCKER_REGISTRY:-kubedb}

IMG=postgres
DB_VERSION=10.6
SUFFIX=v3
TAG="$DB_VERSION-$SUFFIX"

WALG_VER=${WALG_VER:-0.2.13-ac}

DIST="$REPO_ROOT/dist"
mkdir -p "$DIST"

build_binary() {
  make build
}

build_docker() {
  pushd "$REPO_ROOT/hack/docker/postgres/$DB_VERSION"

  # Download wal-g
  wget https://github.com/kubedb/wal-g/releases/download/${WALG_VER}/wal-g-alpine-amd64
  chmod +x wal-g-alpine-amd64
  mv wal-g-alpine-amd64 wal-g

  # Copy pg-operator
  cp "$REPO_ROOT/bin/linux_amd64/pg-operator" pg-operator
  chmod 755 pg-operator

  local cmd="docker build --pull -t $DOCKER_REGISTRY/$IMG:$TAG ."
  echo $cmd; $cmd

  rm wal-g pg-operator
  popd
}

build() {
  build_binary
  build_docker
}

binary_repo $@
