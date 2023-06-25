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

OPERATOR_IMAGE_NAME=${OPERATOR_IMAGE_NAME:-mtq_operator}
CONTROLLER_IMAGE_NAME=${CONTROLLER_IMAGE_NAME:-mtq_controller}
MTQ_LOCK_SERVER_IMAGE_NAME=${MTQ_LOCK_SERVER_IMAGE_NAME:-mtq_lock_server}
DOCKER_PREFIX=${DOCKER_PREFIX:-"quay.io/bmordeha/kubevirt"}
DOCKER_TAG=${DOCKER_TAG:-latest}
VERBOSITY=${VERBOSITY:-1}
PULL_POLICY=${PULL_POLICY:-Always}
MTQ_NAMESPACE=${MTQ_NAMESPACE:-mtq}
CR_NAME=${CR_NAME:-mtq}

# update this whenever new builder tag is created
BUILDER_IMAGE=${BUILDER_IMAGE:-quay.io/bmordeha/kubevirt/kubevirt-mtq-bazel-builder:2306201325-0dfe3ff}

function parseTestOpts() {
    pkgs=""
    test_args=""
    while [[ $# -gt 0 ]] && [[ $1 != "" ]]; do
        case "${1}" in
        --test-args=*)
            test_args="${1#*=}"
            shift 1
            ;;
        ./*...)
            pkgs="${pkgs} ${1}"
            shift 1
            ;;
        *)
            echo "ABORT: Unrecognized option \"$1\""
            exit 1
            ;;
        esac
    done
}