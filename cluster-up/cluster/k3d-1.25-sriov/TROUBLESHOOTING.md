# How to troubleshoot a failing k3d job

If logging and output artifacts are not enough, there is a way to connect to a running CI pod and troubleshoot directly from there.

## Pre-requisites

- A working (enabled) account on the [CI cluster](shift.ovirt.org), specifically enabled to the `kubevirt-prow-jobs` project.
- The [mkpj tool](https://github.com/kubernetes/test-infra/tree/master/prow/cmd/mkpj) installed

## Launching a custom job

Through the `mkpj` tool, it's possible to craft a custom Prow Job that can be executed on the CI cluster. 

Just `go get` it by running `go get k8s.io/test-infra/prow/cmd/mkpj`

Then run the following command from a checkout of the [project-infra repo](https://github.com/kubevirt/project-infra):

```bash
mkpj --pull-number $KUBEVIRT_PR_NUMBER -job pull-kubevirt-e2e-k3d-1.25-sriov -job-config-path github/ci/prow/files/jobs/kubevirt/kubevirt-presubmits.yaml --config-path github/ci/prow/files/config.yaml > debugkind.yaml
```

You will end up having a ProwJob manifest in the `debugkind.yaml` file.

It's strongly recommended to replace the job's name, as it will be easier to find and debug the relative pod, by replacing `metadata.name` with something more recognizeable.

The `$KUBEVIRT_PR_NUMBER` can be an actual PR on the [kubevirt repo](https://github.com/kubevirt/kubevirt).

In case we just want to debug the cluster provided by the CI, it's recommended to override the entry point, either in the test PR we are instrumenting (a good sample can be found [here](https://github.com/kubevirt/kubevirt/pull/3022)), or by overriding the entry point directly in the prow job's manifest.

Remember that we want the cluster long living, so a long sleep must be provided as part of the entry point.

Make sure you switch to the `kubevirt-prow-jobs` project, and apply the manifest:

```bash
kubectl apply -f debugkind.yaml
```

You will end up with a ProwJob object, and a pod with the same name you gave to the ProwJob.

Once the pod is up & running, connect to it via bash:

```bash
kubectl exec -it debugprowjobpod bash
```

### Logistics

Once you are in the pod, you'll be able to troubleshoot what's happening in the environment CI is running its tests.

Run the follow to bring up a [k3d](https://github.com/k3d-io/k3d) cluster with SR-IOV installed.

```bash
KUBEVIRT_PROVIDER=k3d-1.25-sriov make cluster-up
```

Use `k3d kubeconfig print sriov` to extract the kubeconfig file.
The `kubectl` binary is already on board and in `$PATH`.
See `README.md` for more info.
