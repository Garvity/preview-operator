# Architecture and lifecycle

## Source of truth

`PreviewEnvironment.spec` is desired state. The API server assigns its UID and
creation timestamp. The deadline is always recomputed as:

```text
metadata.creationTimestamp + spec.ttlSeconds
```

`status.expiresAt` is an observation, not an input. Changing status cannot extend
the lifetime, nor can restarting the manager. TTL is immutable through a CEL
transition rule. All generated children stay in the CR's namespace.

## Reconcile sequence

1. Fetch the CR. Ignore NotFound and objects already being deleted.
2. Calculate the deadline before considering provisioning.
3. If expired, inspect and delete only owned children with UID preconditions and
   foreground propagation. Wait until both objects disappear before `Expired`.
4. If active, preflight both child names. A foreign object produces `Conflict`
   without adoption. A second ownership check protects each subsequent update.
5. Create or update the Deployment and Service using resource-version-aware
   updates. Conflict errors are retried through the controller workqueue.
6. Publish current rollout status and requeue no later than the deadline, with
   a maximum 30-second resync. Owned child events prompt earlier reconciliation.

The children use the CR's name. Admission restricts names to DNS labels beginning
with a letter and no longer than 63 characters so Service names are valid.
The pod/Service selector uses the CR UID to avoid mixing recreated environments.

## States

| Phase | Meaning |
| --- | --- |
| Pending | Resources exist, but the current rollout is not fully available |
| Ready | Observed Deployment generation is current and total, updated and available replica counts match the desired nonzero count |
| Paused | Desired replicas are zero; the original deadline still applies |
| Error | Kubernetes request failed or Deployment reported rollout failure |
| Conflict | A same-name object is controlled by someone else or is unowned |
| Expiring | Deadline passed and owned resources are still deleting |
| Expired | Deadline passed and no managed Deployment or Service remains |

`Expired=True` means the deadline passed, including during cleanup failures.
`Ready=False` carries the detailed reason. Conditions use the CR generation and
preserve transition timestamps unless their boolean status changes.

## Ownership and deletion

Owner references make Kubernetes garbage collection responsible for explicit CR
deletion. Expiry retains the CR and explicitly removes its children. No finalizer
is necessary on the CR because this release does not provision external resources.
As with other owner-referenced Kubernetes objects, an explicit orphan deletion
can leave resources behind; ordinary deletion is the supported cleanup workflow.

During expiry, a foreign child is left untouched and reported as a conflict while
other owned children can still be cleaned up. Child UID preconditions prevent a
replacement object from being deleted after a stale read. Resource-version-aware
updates prevent replacement races from turning into accidental adoption.

Foreground deletion means a stuck Pod, custom finalizer, or unavailable garbage
collector can leave the environment in `Expiring`. The operator deliberately does
not remove other controllers' finalizers or claim cleanup completed early.

## Drift, defaults and availability

The controller owns Deployment spec, managed labels and Service routing. Direct
edits to those fields are reverted; changes should go through the CR. Kubernetes
defaulted fields are explicitly accounted for so repeated reconciles do not write
unchanged objects. The allocated Service ClusterIP and IP families are preserved.

Rolling updates allow one extra replica and zero unavailable replicas. Capacity
must accommodate the surge. Readiness is TCP-based. A Deployment exceeding its
120-second progress deadline produces an Error condition, with the Kubernetes
message available for diagnosis.

Leader election permits multiple manager replicas but does not make the cluster
itself highly available. Expiry is eventual: it depends on manager availability,
API access and Kubernetes garbage collection. If the manager is down, resources
can outlive their deadline and are cleaned up on its return.

## Extension roadmap

Before adding PR webhooks or Ingress, design authentication, resource quotas,
network isolation, URL lifecycle and ingress-controller compatibility. External
resources would need finalizers, idempotent provider operations and explicit
recovery semantics. Keep these additions separate from the current small API.

## References

- [Kubernetes sample controller](https://github.com/kubernetes/sample-controller)
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)
- [Kubebuilder envtest](https://book.kubebuilder.io/reference/envtest.html)
