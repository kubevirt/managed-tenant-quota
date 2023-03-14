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
reasons not reflected to the VM creator / namespace owner.


### KubeVirt Upgrade:

When upgrading KubeVirt, it is necessary to migrate VMIs to upgrade the
virt-launcher pod.
However, migrating a virtual machine in the presence of a resource quota
can cause the migration to fail and subsequently cause the upgrade to fail.

## Managed-Tenant-Quota

To ensure an efficient use of resources, an "operational" quota is provided
to tenants by the administrator.
This quota is temporarily used to admit pods such as a live-migration target pod.
When VirtualMachineInstanceMigration is blocked by resourceQuota, the Managed
Quota controller will lock the namespace, increase the quota, admit the target
pod, decrease the namespace quota, and unlock the namespace.
The namespace will only remain locked and the quota altered for a short period
of time, until the target pod is admitted.

The locking of the namespace will be enforced by a validation webhook with
a namespace selector and an object selector. The webhook will reject any
creation of objects in the namespace except for the target pods.

Instead of an admin creating a quota object, a VirtualMachineMigrationResourceQuota
(VMMRQ) CR will be created in a namespace to define the maximum resources allowed
to be exceeded during migrations. The VMMRQ manager will watch quotas in the
namespace and will alter it when required for admitting a target pod.

This approach ensures efficient use of resources, as a fixed amount of additional
resources will be set aside for all migrations, rather than consuming extra
resources in each individual namespace.
