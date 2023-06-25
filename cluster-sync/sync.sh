#!/bin/bash -e

mtq=$1
mtq="${mtq##*/}"

echo mtq

source ./hack/build/config.sh
source ./hack/build/common.sh
source ./cluster-up/hack/common.sh
source ./cluster-up/cluster/${KUBEVIRT_PROVIDER}/provider.sh

if [ "${KUBEVIRT_PROVIDER}" = "external" ]; then
   MTQ_SYNC_PROVIDER="external"
else
   MTQ_SYNC_PROVIDER="kubevirtci"
fi
source ./cluster-sync/${MTQ_SYNC_PROVIDER}/provider.sh


MTQ_NAMESPACE=${MTQ_NAMESPACE:-mtq}
MTQ_INSTALL_TIMEOUT=${MTQ_INSTALL_TIMEOUT:-120}
MTQ_AVAILABLE_TIMEOUT=${MTQ_AVAILABLE_TIMEOUT:-600}
MTQ_PODS_UPDATE_TIMEOUT=${MTQ_PODS_UPDATE_TIMEOUT:-480}
MTQ_UPGRADE_RETRY_COUNT=${MTQ_UPGRADE_RETRY_COUNT:-60}

# Set controller verbosity to 3 for functional tests.
export VERBOSITY=3

PULL_POLICY=${PULL_POLICY:-IfNotPresent}
# The default DOCKER_PREFIX is set to kubevirt and used for builds, however we don't use that for cluster-sync
# instead we use a local registry; so here we'll check for anything != "external"
# wel also confuse this by swapping the setting of the DOCKER_PREFIX variable around based on it's context, for
# build and push it's localhost, but for manifests, we sneak in a change to point a registry container on the
# kubernetes cluster.  So, we introduced this MANIFEST_REGISTRY variable specifically to deal with that and not
# have to refactor/rewrite any of the code that works currently.
MANIFEST_REGISTRY=$DOCKER_PREFIX

if [ "${KUBEVIRT_PROVIDER}" != "external" ]; then
  registry=${IMAGE_REGISTRY:-localhost:$(_port registry)}
  DOCKER_PREFIX=${registry}
  MANIFEST_REGISTRY="registry:5000"
fi

if [ "${KUBEVIRT_PROVIDER}" == "external" ]; then
  # No kubevirtci local registry, likely using something external
  if [[ $(${MTQ_CRI} login --help | grep authfile) ]]; then
    registry_provider=$(echo "$DOCKER_PREFIX" | cut -d '/' -f 1)
    echo "Please log in to "${registry_provider}", bazel push expects external registry creds to be in ~/.docker/config.json"
    ${MTQ_CRI} login --authfile "${HOME}/.docker/config.json" $registry_provider
  fi
fi

# Need to set the DOCKER_PREFIX appropriately in the call to `make docker push`, otherwise make will just pass in the default `kubevirt`

DOCKER_PREFIX=$MANIFEST_REGISTRY PULL_POLICY=$PULL_POLICY make manifests
DOCKER_PREFIX=$DOCKER_PREFIX make push


function check_structural_schema {
  for crd in "$@"; do
    status=$(_kubectl get crd $crd -o jsonpath={.status.conditions[?\(@.type==\"NonStructuralSchema\"\)].status})
    if [ "$status" == "True" ]; then
      echo "ERROR CRD $crd is not a structural schema!, please fix"
      _kubectl get crd $crd -o yaml
      exit 1
    fi
    echo "CRD $crd is a StructuralSchema"
  done
}

function wait_mtq_available {
  echo "Waiting $MTQ_AVAILABLE_TIMEOUT seconds for MTQ to become available"
  if [ "$KUBEVIRT_PROVIDER" == "os-3.11.0-crio" ]; then
    echo "Openshift 3.11 provider"
    available=$(_kubectl get mtq mtq -o jsonpath={.status.conditions[0].status})
    wait_time=0
    while [[ $available != "True" ]] && [[ $wait_time -lt ${MTQ_AVAILABLE_TIMEOUT} ]]; do
      wait_time=$((wait_time + 5))
      sleep 5
      sleep 5
      available=$(_kubectl get mtq mtq -o jsonpath={.status.conditions[0].status})
      fix_failed_sdn_pods
    done
  else
    _kubectl wait mtqs.mtq.kubevirt.io/${CR_NAME} --for=condition=Available --timeout=${MTQ_AVAILABLE_TIMEOUT}s
  fi
}


OLD_MTQ_VER_PODS="./_out/tests/old_mtq_ver_pods"
NEW_MTQ_VER_PODS="./_out/tests/new_mtq_ver_pods"

mkdir -p ./_out/tests
rm -f $OLD_MTQ_VER_PODS $NEW_MTQ_VER_PODS

# Install MTQ
install_mtq

#wait mtq crd is installed with timeout
wait_mtq_crd_installed $MTQ_INSTALL_TIMEOUT


_kubectl apply -f "./_out/manifests/release/mtq-cr.yaml"
wait_mtq_available



# Grab all the MTQ crds so we can check if they are structural schemas
mtq_crds=$(_kubectl get crd -l mtq.kubevirt.io -o jsonpath={.items[*].metadata.name})
crds=($mtq_crds)
operator_crds=$(_kubectl get crd -l operator.mtq.kubevirt.io -o jsonpath={.items[*].metadata.name})
crds+=($operator_crds)
check_structural_schema "${crds[@]}"
