#!/usr/bin/env bash

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

set -e

script_dir="$(readlink -f $(dirname $0))"
source "${script_dir}"/common.sh
source "${script_dir}"/config.sh

docker_opt="${1:-build}"

PUSH_TARGETS=(${PUSH_TARGETS:-$CONTROLLER_IMAGE_NAME $MTQ_LOCK_SERVER_IMAGE_NAME $OPERATOR_IMAGE_NAME})
echo "docker_prefix: $DOCKER_PREFIX, docker_tag: $DOCKER_TAG"
for target in ${PUSH_TARGETS[@]}; do
    BIN_NAME="${target}"
    IMAGE="${DOCKER_PREFIX}/${BIN_NAME}:${DOCKER_TAG}"

    if [ "${docker_opt}" == "build" ]; then
        (
        pwd
            docker "${docker_opt}" -t ${IMAGE} . -f Dockerfile.${BIN_NAME}
        )
    elif [ "${docker_opt}" == "push" ]; then
        docker "${docker_opt}" "${IMAGE}"
    elif [ "${docker_opt}" == "publish" ]; then
        docker push ${IMAGE}
    fi
done