# KubeVirt Managed-Tenant-Quota

**Please keep in mind the project still requires thorough testing.**

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

Our system employs a Managed Quota controller to optimize resource utilization and streamline the migration process.
Here's how it works:

Tenants are provided with a namespaced Custom Resource called VirtualMachineMigrationResourceQuota.
This CR allows temporary resource allocation for admitting live-migration target pods.

**When a VirtualMachineInstanceMigration encounters resourceQuota limitations, the controller takes the following steps:**

1. If unlocking is feasible, the controller locks the namespace, ensuring exclusive resource 
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

**The benefits of our Managed Quota controller include:**

1. Optimized Resource Utilization: It allocates a fixed additional resource capacity for all migrations within a namespace, 
minimizing resource consumption in individual namespaces and maximizing availability.

2. Seamless Migration Experience: The controller dynamically adjusts quotas and promptly admits target pods,
minimizing downtime and ensuring a smooth migration process.

3. Enhanced Control and Efficiency: Administrators gain granular control over resource allocation using 
VirtualMachineMigrationResourceQuota CRs and the controller. This automated approach simplifies management, 
improves efficiency, and streamlines migrations.

In summary, our Managed Quota controller optimizes resource utilization, facilitates seamless migrations, 
and provides enhanced control and efficiency. It brings significant benefits in terms of
operational efficiency, and successful migrations.



