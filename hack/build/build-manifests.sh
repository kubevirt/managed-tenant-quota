#!/usr/bin/env bash



script_dir="$(cd "$(dirname "$0")" && pwd -P)"
source "${script_dir}"/common.sh

generator="${BIN_DIR}/manifest-generator"


(cd "${MTQ_DIR}/tools/manifest-generator/" &&  go build -o "${generator}" ./...)

echo "DOCKER_PREFIX=${DOCKER_PREFIX}"
echo "DOCKER_TAG=${DOCKER_TAG}"
echo "VERBOSITY=${VERBOSITY}"
echo "PULL_POLICY=${PULL_POLICY}"
echo "MTQ_NAMESPACE=${MTQ_NAMESPACE}"
source "${script_dir}"/resource-generator.sh

mkdir -p "${MANIFEST_GENERATED_DIR}/"


#generate operator related manifests used to deploy mtq with operator-framework
generateResourceManifest $generator $MANIFEST_GENERATED_DIR "operator" "everything" "operator-everything.yaml.in"

#process templated manifests and populate them with generated manifests
tempDir=${MANIFEST_TEMPLATE_DIR}
processDirTemplates ${tempDir} ${OUT_DIR}/manifests ${OUT_DIR}/manifests/templates ${generator} ${MANIFEST_GENERATED_DIR}
processDirTemplates ${tempDir}/release ${OUT_DIR}/manifests/release ${OUT_DIR}/manifests/templates/release ${generator} ${MANIFEST_GENERATED_DIR}

testsManifestsDir=${MTQ_DIR}/tests/manifests
processDirTemplates ${testsManifestsDir}/templates ${testsManifestsDir}/out ${testsManifestsDir}/out/templates ${generator} ${MANIFEST_GENERATED_DIR}
