# KubeVirt Managed-Tenant-Quota
## Context:

In a multi-tenant environment, a cluster administrator creates a namespace
for each tenant and controls the amount of resources that they can use by
creating a ResourceQuota object within the namespace.

## Motivation:

### Simple migration:

During the migration process, there is a hidden cost involved in creating a
new virt-launcher pod before terminating the old one.
This differs from how standard pods are moved and can result in unexpected
resource utilization from the user's perspective. This poses a challenge
where the hidden cost of live migration can prevent live migrating a VM for
reasons not reflected to the VM creator / namespace owner


### KubeVirt Upgrade:

When upgrading KubeVirt, it is necessary to migrate VMIs to upgrade the
virt-launcher pod.
However, migrating a virtual machine in the presence of a resource quota
can cause the migration to fail and subsequently cause the upgrade to fail.

## Managed-Tenant-Quota

Our system employs a Managed Quota controller to optimize resource utilization and streamline the migration process.
Here's how it works:

Tenants are provided with a namespaced Custom Resource called VirtualMachineMigrationResourceQuota.
This CR allows temporary resource allocation for admitting live-migration target pods.

### Deploy it on your cluster

Deploying the MTQ controller is straightforward. 

  ```
  $ export VERSION=$(curl -s https://api.github.com/repos/kubevirt/managed-tenant-quota/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
  $ kubectl create -f https://github.com/kubevirt/managed-tenant-quota/releases/download/$VERSION/mtq-operator.yaml
  $ kubectl create -f https://github.com/kubevirt/managed-tenant-quota/releases/download/$VERSION/mtq-cr.yaml
  ```


### Deploy it with our CI system

MTQ includes a self contained development and test environment.  We use Docker to build, and we provide a simple way to get a test cluster up and running on your laptop. The development tools include a version of kubectl that you can use to communicate with the cluster. A wrapper script to communicate with the cluster can be invoked using ./cluster-up/kubectl.sh.

```bash
$ mkdir $GOPATH/src/kubevirt.io && cd $GOPATH/src/kubevirt.io
$ git clone https://github.com/kubevirt/managed-tenant-quota && cd managed-tenant-quota
$ make cluster-up
$ make cluster-sync
$ ./cluster-up/kubectl.sh .....
```
For development on external cluster (not provisioned by our MTQ),
check out the [external provider](cluster-sync/external/README.md).


### VirtualMachineMigrationResourceQuota
**When a VirtualMachineInstanceMigration encounters resourceQuota limitations and VirtualMachineMigrationResourceQuota is deployed in the namespace, the controller takes the following steps:**

1. If at least a single blocked migration resources meet the VirtualMachineMigrationResourceQuota resource limitations, the controller locks the namespace by creating dedicated validatingWebhookConfiguration, ensuring exclusive resource 
allocation for the migration.

2. Dynamically adjusts the blocking resourceQuotas within the namespace, using the resources 
specified in the associated VirtualMachineMigrationResourceQuota.

3. Admits the target pod, facilitating a seamless migration process.

4. After creating the target pod, the controller promptly reduces the namespace quota and unlocks the namespace.

The controller generates an event if unlocking is not feasible and reevaluate for changes, allowing for appropriate issue resolution.

To maintain consistency and avoid conflicts, resourceQuotas in a namespace can only be updated by the Managed Quota controller 
when the namespace is locked. 
Additionally, the VirtualMachineMigrationResourceQuota in the locked namespace cannot be 
deleted to preserve the migration process integrity.


An illustrative instance of a valid VirtualMachineMigrationResourceQuota is available at:
https://github.com/kubevirt/managed-tenant-quota/blob/main/manifests/VirtualMachineMigrationResourceQuotaExample.yaml


VirtualMachineMigrationResourceQuota has only single spec called `additionalMigrationResources`.

for the sake of simplicity and convenient `VirtualMachineMigrationResourceQuota.spec.additionalMigrationResources` field has the same type as the `ResourceQuota.spec.hard` field 
that is used to define resources limitations.


While `VirtualMachineMigrationResourceQuota.spec.additionalMigrationResources` field represent the desired state the actual state is represented by
`VirtualMachineMigrationResourceQuota.spec.AdditionalMigrationResources` similar to it works with `ResourceQuota.spec.hard` field in ResourceQuota.

### summary

Our Managed Quota controller optimizes resource utilization, facilitates seamless migrations, 
and provides enhanced control and efficiency. It brings significant benefits in terms of
operational efficiency, and successful migrations.



