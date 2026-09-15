# Operations runbook

## Install into a team namespace

The default kustomization installs the CRD cluster-wide and a manager with Role/
RoleBinding in `preview-system`. CRD installation requires cluster administrator
permissions. Runtime permissions remain namespace-scoped. The namespace enforces
Kubernetes restricted Pod Security admission for the verified version.

Use a kustomize overlay to replace the namespace, Namespace object's name,
RoleBinding subject namespace and image together. The manager reads the namespace
from the downward API. Confirm the rendered RoleBinding namespace and subject
before applying. Do not point the manager at a namespace without matching RBAC.

For a shared cluster, build/push a versioned image to your registry and replace the
image reference in your overlay with its immutable digest. No image is automatically
pushed by this repository. Base images are pinned by digest. The local kind build
disables attestations for archive compatibility; configure provenance, SBOM generation
and signing separately in your registry publication workflow. Set resource requests/limits and quotas according to
measured workload volume. Controller memory scales with cached resources.

## Observe

```bash
kubectl get preview -n preview-system
kubectl describe preview hello-preview -n preview-system
kubectl logs deployment/preview-operator -n preview-system --tail=100
kubectl get events -n preview-system --sort-by=.lastTimestamp
```

Manager liveness is `/healthz`; readiness is `/readyz` and waits for cache sync.
The default health listener is `:8081`. Prometheus metrics bind to
`127.0.0.1:8080`; there is no public metrics Service.

```bash
kubectl port-forward deployment/preview-operator -n preview-system 9090:8080
curl http://127.0.0.1:9090/metrics
```

Inspect `controller_runtime_reconcile_errors_total`, reconciliation duration and
`preview_operator_phase_transitions_total{phase="..."}`. The latter counts observed
transitions, not unique environments, and resets on process restart. It has no
environment, namespace or image labels. To expose metrics beyond loopback, add
network restrictions and an authenticated proxy/transport appropriate to your
monitoring system; the listener itself does not add authentication.

## Troubleshoot

| Symptom | Checks and resolution |
| --- | --- |
| No status | Manager readiness, watch namespace, CRD installation, RoleBinding, leader lease |
| Pending | Pod events, image availability, actual listening port, UID 10001 compatibility, resource capacity |
| Error / RolloutFailed | Deployment progress condition, crashed pods, writable paths, admission rejection |
| Conflict | Identify the foreign Deployment/Service; choose a new preview name or have its owner remove it |
| Expiring for a long time | Child deletionTimestamp, finalizers, remaining Pods, garbage collector health |
| Resources exist past deadline | Manager/API downtime; compare creationTimestamp plus TTL and restore controller availability |
| Spec edit does not stick on a child | Edit the PreviewEnvironment instead; the controller repairs managed fields |

An invalid resource budget can be accepted on the CR but rejected when the child
Deployment is created. The failure is published in status and retried with backoff;
correct `spec.resources`. No webhook is required for the current API.

## Upgrade and rollback

Review the CRD diff and release notes before updating. This `v1alpha1` API is not
promised stable; back up CR specs and inventory active previews before migration.
Apply compatible CRD changes first, then update the manager image. Existing TTLs
remain anchored to original creation times. Use `kubectl rollout status` and
observe reconciliation errors. Roll back the image only when its API expectations
remain compatible with the installed CRD; do not blindly roll back stored schemas.

## Uninstall

Delete preview CRs and verify their workloads are gone while the operator and
garbage collector are available. Then remove the manager/RBAC. Delete the CRD only
after checking all namespaces for remaining CRs. Deleting the CRD removes all CRs;
deleting the default Namespace removes everything in that namespace. For a local
disposable demo, delete the dedicated kind cluster instead.

## Limitations

- Verified on Kubernetes 1.37, Go 1.27.1, macOS arm64 host and Linux arm64 kind node.
  CI is configured for Linux amd64 but its remote runs are not implied by local tests.
- One trusted namespace per manager deployment. Creating Deployments is a powerful
  permission; namespace RBAC alone is not safe isolation between hostile tenants.
- No ingress, DNS, certificates, secrets, persistent storage, imagePullSecrets,
  PR webhooks, external cloud resources, autoscaling or per-environment quotas.
- Restricted container defaults require compatible images. No general pod-template
  escape hatch is exposed. NetworkPolicy and ResourceQuota are platform concerns.
- TCP readiness is not application correctness; only HTTP workloads have a useful
  `http://` endpoint. There is no application liveness probe in this release.
- Expiry is eventual, not a hard wall-clock resource guarantee. Stuck finalizers,
  node failures, manager outages and API outages can delay cleanup.
- Sample images use version tags for readability. Pin application digests for
  reproducibility and establish image scanning/signing policies for deployments.
- No load/soak test, independent security audit, cloud HA exercise or hosted release
  has been performed as part of the initial implementation.
