#!/usr/bin/env bash

set -e
MTQ_INSTALL_TIMEOUT=${MTQ_INSTALL_TIMEOUT:-120}     #timeout for installation sequence

function install_mtq {
  _kubectl apply -f "./_out/manifests/release/mtq-operator.yaml"
}

function wait_mtq_crd_installed {
  timeout=$1
  crd_defined=0
  while [ $crd_defined -eq 0 ] && [ $timeout > 0 ]; do
      crd_defined=$(_kubectl get customresourcedefinition| grep mtqs.mtq.kubevirt.io | wc -l)
      sleep 1
      timeout=$(($timeout-1))
  done

  #In case MTQ crd is not defined after 120s - throw error
  if [ $crd_defined -eq 0 ]; then
     echo "ERROR - MTQ CRD is not defined after timeout"
     exit 1
  fi  
}

