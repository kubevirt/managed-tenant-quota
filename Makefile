#Copyright 2023 The MTQ Authors.
#
#Licensed under the Apache License, Version 2.0 (the "License");
#you may not use this file except in compliance with the License.
#You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
#Unless required by applicable law or agreed to in writing, software
#distributed under the License is distributed on an "AS IS" BASIS,
#WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#See the License for the specific language governing permissions and
#limitations under the License.

.PHONY: manifests \
		cluster-up cluster-down cluster-sync \
		test test-functional test-unit test-lint \
		publish \
		mtq_controller \
		mtq_lock_server \
		mtq_operator \
		fmt \
		goveralls \
		release-description \
		bazel-build-images push-images \
		fossa
all: build

build:  mtq_controller mtq_lock_server mtq_operator

DOCKER?=1
ifeq (${DOCKER}, 1)
	# use entrypoint.sh (default) as your entrypoint into the container
	DO=./hack/build/in-docker.sh
	# use entrypoint-bazel.sh as your entrypoint into the container.
	DO_BAZ=./hack/build/bazel-docker.sh
else
	DO=eval
	DO_BAZ=eval
endif

all: manifests bazel-build-images

manifests:
	${DO_BAZ} "DOCKER_PREFIX=${DOCKER_PREFIX} DOCKER_TAG=${DOCKER_TAG} VERBOSITY=${VERBOSITY} PULL_POLICY=${PULL_POLICY} CR_NAME=${CR_NAME} MTQ_NAMESPACE=${MTQ_NAMESPACE} ./hack/build/build-manifests.sh"

builder-push:
	./hack/build/bazel-build-builder.sh

generate:
	${DO_BAZ} "./hack/update-codegen.sh"

cluster-up:
	./cluster-up/up.sh

cluster-down:
	./cluster-up/down.sh

push-images:
	eval "DOCKER_PREFIX=${DOCKER_PREFIX} DOCKER_TAG=${DOCKER_TAG}  ./hack/build/build-docker.sh push"

build-images:
	eval "DOCKER_PREFIX=${DOCKER_PREFIX} DOCKER_TAG=${DOCKER_TAG}  ./hack/build/build-docker.sh"

push: build-images push-images

cluster-clean-mtq:
	./cluster-sync/clean.sh

cluster-sync-mtq: cluster-clean-mtq
	./cluster-sync/sync.sh MTQ_AVAILABLE_TIMEOUT=${MTQ_AVAILABLE_TIMEOUT} DOCKER_PREFIX=${DOCKER_PREFIX} DOCKER_TAG=${DOCKER_TAG} PULL_POLICY=${PULL_POLICY} MTQ_NAMESPACE=${MTQ_NAMESPACE}

cluster-sync: cluster-sync-mtq

test: WHAT = ./pkg/... ./cmd/...
test: bootstrap-ginkgo
	${DO_BAZ} "ACK_GINKGO_DEPRECATIONS=${ACK_GINKGO_DEPRECATIONS} ./hack/build/run-unit-tests.sh ${WHAT}"

build-functest:
	${DO_BAZ} ./hack/build/build-functest.sh

functest:  WHAT = ./tests/...
functest: build-functest
	./hack/build/run-functional-tests.sh ${WHAT} "${TEST_ARGS}"

bootstrap-ginkgo:
	${DO_BAZ} ./hack/build/bootstrap-ginkgo.sh

mtq_controller:
	go build -o mtq_controller -v cmd/mtq-controller/*.go
	chmod 777 mtq_controller

mtq_operator:
	go build -o mtq_operator -v cmd/mtq-operator/*.go
	chmod 777 mtq_operator

mtq_lock_server:
	go build -o mtq_lock_server -v cmd/mtq-lock-server/*.go
	chmod 777 mtq_lock_server

clean:
	rm ./mtq_controller ./mtq_operator ./mtq_lock_server -f

dist-clean: clean
	docker rmi -f `docker images 'quay.io/bmordeha/kubevirt/mtq_controller' -a -q`
	docker rmi -f `docker images 'quay.io/bmordeha/kubevirt/mtq_lock_server' -a -q`
	docker rmi -f `docker images 'quay.io/bmordeha/kubevirt/mtq_operator' -a -q`

fmt:
	go fmt .

run: build
	sudo ./mtq_controller
