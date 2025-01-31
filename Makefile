# Copyright 2014 Canonical Ltd.
# Makefile for the JIMM service.

export GO111MODULE=on
export DOCKER_BUILDKIT=1

PROJECT := github.com/canonical/jimm

GIT_COMMIT := $(shell git rev-parse --verify HEAD)
GIT_VERSION := $(shell git describe --abbrev=0 --dirty)
GO_VERSION := $(shell go list -f {{.GoVersion}} -m)
ARCH := $(shell dpkg --print-architecture)

default: build

build: version/commit.txt version/version.txt
	go build -tags version $(PROJECT)/...

build/server: version/commit.txt version/version.txt
	go build -tags version ./cmd/jimmsrv

lint:
	golangci-lint run --timeout 5m

check: version/commit.txt version/version.txt lint
	go test -timeout 30m $(PROJECT)/... -cover

# generates database schemas locally to inspect them.
generate-schemas:
	@./local/jimm/generate_db_schemas.sh

clean:
	go clean $(PROJECT)/...
	-$(RM) version/commit.txt version/version.txt
	-$(RM) jimmsrv
	-$(RM) -r jimm-release/
	-$(RM) jimm-*.tar.xz

certs:
	@cd local/traefik/certs; ./certs.sh; cd -

test-env: sys-deps
	@docker compose up --force-recreate -d --wait

test-env-cleanup:
	@docker compose down -v --remove-orphans

dev-env-setup: sys-deps certs
	@make version/commit.txt && make version/version.txt

dev-env: dev-env-setup
	@docker compose --profile dev up -d --force-recreate --wait

dev-env-cleanup:
	@docker compose --profile dev down -v --remove-orphans

qa-microk8s:
	@./local/jimm/qa-microk8s.sh

qa-lxd:
	@./local/jimm/qa-lxd.sh

integration-test-env: dev-env-setup
	@JIMM_VERSION=$(JIMM_VERSION) docker compose --profile test up -d --force-recreate --wait

# Reformat all source files.
format:
	gofmt -w -l .

# Reformat and simplify source files.
simplify:
	gofmt -w -l -s .

# Generate version information
version/commit.txt:
	git rev-parse --verify HEAD > version/commit.txt

version/version.txt:
	if [ -z "$(GIT_VERSION)" ]; then \
        echo "dev" > version/version.txt; \
    else \
        echo $(GIT_VERSION) > version/version.txt; \
    fi

jimm-image:
	docker build --target deploy-env \
	--build-arg="GIT_COMMIT=$(GIT_COMMIT)" \
	--build-arg="VERSION=$(GIT_VERSION)" \
	--build-arg="GO_VERSION=$(GO_VERSION)" \
	--build-arg="ARCH=$(ARCH)" \
	--tag jimm:latest .

jimm-snap:
	mkdir -p ./snap
	cp ./snaps/jimm/snapcraft.yaml ./snap/
	snapcraft

jimmctl-snap:
	mkdir -p ./snap
	cp -R ./snaps/jimmctl/* ./snap/
	snapcraft

jaas-snap:
	mkdir -p ./snap
	cp -R ./snaps/jaas/* ./snap/
	snapcraft

push-microk8s: jimm-image
	docker tag jimm:latest localhost:32000/jimm:latest
	docker push localhost:32000/jimm:latest

rock:
	-rm *.rock
	-ln -s ./rocks/jimm.yaml ./rockcraft.yaml
	rockcraft pack
	-rm ./rockcraft.yaml

load-rock: 
	$(eval jimm_version := $(shell cat ./rocks/jimm.yaml | yq ".version"))
	@sudo /snap/rockcraft/current/bin/skopeo --insecure-policy copy oci-archive:jimm_${jimm_version}_amd64.rock docker-daemon:jimm:latest

test-auth-model:
	fga model test --tests ./openfga/tests.fga.yaml 

define check_dep
    if ! which $(1) > /dev/null; then\
		echo "$(2)";\
	else\
		echo "Detected $(1)";\
	fi
endef

# Install packages required to develop JIMM and/or run tests.
APT_BASED := $(shell command -v apt-get >/dev/null; echo $$?)
sys-deps:
ifeq ($(APT_BASED),0)
# fga is required for openfga tests
	@$(call check_deps,fga,Missing FGA client - install via 'go install github.com/openfga/cli/cmd/fga@latest')
# golangci-lint is necessary for linting.
	@$(call check_dep,golangci-lint,Missing Golangci-lint - install from https://golangci-lint.run/welcome/install/ or 'sudo snap install golangci-lint --classic')
# Go acts as the test runner.
	@$(call check_dep,go,Missing Go - install from https://go.dev/doc/install or 'sudo snap install go --classic')
# Git is useful to have.
	@$(call check_dep,git,Missing Git - install with 'sudo apt install git')
# GCC is required for the compilation process.
	@$(call check_dep,gcc,Missing gcc - install with 'sudo apt install build-essential')
# yq is necessary for some scripts that process controller-info yaml files.
	@$(call check_dep,yq,Missing yq - install with 'sudo snap install yq')
# Microk8s is required if you want to start a Juju controller on Microk8s.
	@$(call check_dep,microk8s,Missing microk8s - install with 'sudo snap install microk8s --channel=1.30-strict/stable')
# Docker is required to start the test dependencies in containers.
	@$(call check_dep,docker,Missing Docker - install from https://docs.docker.com/engine/install/ or 'sudo snap install docker')
# juju-db is required for tests that use Juju's test fixture, requiring MongoDB.
	@$(call check_dep,juju-db.mongo,Missing juju-db - install with 'sudo snap install juju-db --channel=4.4/stable')
else
	@echo sys-deps runs only on systems with apt-get
	@echo on OS X with homebrew try: brew install bazaar mongodb
endif

help:
	@echo -e 'JIMM - list of make targets:\n'
	@echo 'make - Build the package.'
	@echo 'make check - Run tests.'
	@echo 'make install - Install the package.'
	@echo 'make server - Start the JIMM server.'
	@echo 'make clean - Remove object files from package source directories.'
	@echo 'make sys-deps - Install the development environment system packages.'
	@echo 'make format - Format the source files.'
	@echo 'make simplify - Format and simplify the source files.'
	@echo 'make rock - Build the JIMM rock.'
	@echo 'make load-rock - Load the most recently built rock into your local docker daemon.'

.PHONY: build check install release clean format server simplify sys-deps help
