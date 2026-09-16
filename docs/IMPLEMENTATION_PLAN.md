# Preview Operator implementation plan

## Objective and acceptance contract

Build a namespaced Kubernetes operator in Go that turns a `PreviewEnvironment`
custom resource into a Deployment and ClusterIP Service. It must restore deleted
resources, converge after edits, report readiness accurately, and remove workloads
after a fixed TTL even when the operator restarts. This is an operationally hardened
portfolio release, not a claim of certification or production deployment.

The first release supports one application container per environment, configurable
image/arguments/ports/replicas/resources, and an immutable TTL measured from the API
server creation timestamp. The CR remains as an expired audit record. Namespaces,
Ingress, DNS, TLS certificates, databases, PR webhooks, arbitrary pod templates,
secret management, multi-tenancy, and external cloud resources are outside v1.

## Design decisions

- API: `preview.garv.dev/v1alpha1`, kind `PreviewEnvironment`, namespaced; generated
  structural CRD with defaults, CEL validation and status subresource.
- Runtime: pinned controller-runtime and matching Kubernetes libraries. Generated
  deepcopy code and manifests must be reproducible.
- Ownership: owner references tie each child to the CR UID. Never adopt, overwrite,
  or delete a same-name object belonging to another owner. UID preconditions protect
  deletion against replacement races. Garbage collection handles CR deletion;
  there are no external resources requiring a finalizer.
- Reconciliation: deterministic desired state; repair drift and missing children;
  watch owned Deployments/Services; requeue at expiry and during deletion. Avoid
  unnecessary status writes. Ready depends on observed deployment generation and
  available/updated replicas. Expiration takes precedence over provisioning.
- Time: derive deadline from creation time and immutable TTL; never use an in-memory
  timer as the source of truth. Expired environments never recreate children.
- Security: watch one configured namespace; namespace-scoped RBAC, no Secret access;
  non-root containers, RuntimeDefault seccomp, dropped capabilities, no privilege
  escalation, read-only root filesystem, application service-account token disabled.
  Untrusted workloads still require admission policies, quotas and network policy.
- Operations: leader election, graceful termination, health/readiness probes,
  structured logging, status conditions, Kubernetes Events and bounded-label metrics.
  Metrics bind to loopback by default; exposure is an explicit operational choice.
- Testing: unit tests for lifecycle/errors/ownership/time; real API-server integration
  for CRD admission and reconciliation; isolated kind end-to-end tests for workload
  readiness, self-healing, operator restart, expiry and garbage collection.

## Milestones and commit gates

| Milestone | Deliverables | Acceptance gate |
| --- | --- | --- |
| M0 — Plan | This plan, initial repository hygiene, commit schedule | Scope and verification criteria recorded before code |
| M1 — API | Go module, API types, defaults, validation, generated CRD/deepcopy | Generation and API tests pass |
| M2 — Controller | Ownership-safe reconciliation, expiry, readiness, metrics, lifecycle tests | Controller tests and race detector pass |
| M3 — Operations | Manager, namespaced RBAC, kustomize, Dockerfile, Make targets, CI | Build, vet, manifest rendering and generated-file checks pass |
| M4 — Integration | API-server tests, kind test harness and live deployment | CRD validation, repair, restart/expiry and deletion verified |
| M5 — Delivery | README, architecture, runbook, contribution/security docs, evidence | Fresh documented commands work; checks pass; final Git status clean |

Every milestone receives a separate descriptive Git commit. Author and committer
timestamps are user-requested, randomly selected and ordered between September 14,
2026 00:00 and September 15, 2026 at the start of this build (Asia/Kolkata). These
timestamps are presentation metadata; test evidence records actual execution time.

## Verification and delivery

1. Run formatting, `go vet`, unit tests with race detection and coverage.
2. Regenerate CRD/deepcopy; require no unexplained generated-file drift.
3. Start envtest API server and etcd; test schema defaults, constraints, immutable TTL,
   actual controller convergence, status and expiry. Do not use envtest to claim
   kubelet readiness or garbage collection: it has neither.
4. Build a non-root image and create a dedicated `preview-operator-e2e` kind cluster,
   using a dedicated kubeconfig so the user's current context remains unchanged.
5. Install manifests and create examples. Check HTTP response, recovery after child
   deletion, spec changes, no cross-owner adoption, expiry after manager restart,
   and owner-reference garbage collection.
6. Save machine-readable/text evidence under `docs/verification/`; save all visual
   output, including the final project summary, only under `output-images/`.
   Visuals must distinguish observed test results from architectural explanation.
7. Document actual versions, measured results, reproduction commands and remaining
   limitations. If a dependency or permission blocks a gate, state that explicitly.

## Progress

- [x] M0 — Plan and repository initialized.
- [x] M1 — API: generated structural CRD/deepcopy and API tests pass.
- [x] M2 — Controller: lifecycle, ownership, expiry and race tests pass (86.3% controller coverage).
- [x] M3 — Operations: manager build, vet, race tests, kustomize rendering and generated-file checks pass.
- [x] M4 — Integration: real API-server tests and all eight kind lifecycle checks pass; isolated cluster cleaned up.
- [x] M5 — Delivery: README, architecture, runbook, contribution/security docs and verified SVG/PNG outputs delivered; documentation links and repository hygiene checked.

## Follow-up feature audit — September 16, 2026

- [x] Re-run build, race tests, schema generation and CLI checks.
- [x] Reproduce and fix Deployment rollout-pause/readiness-delay drift.
- [x] Verify all 18 live feature checks and API-server regressions.
- [x] Refresh the report, coverage and images; remove the test cluster.

See `docs/verification/FEATURE_AUDIT.md` for the measured outcomes and limitations.
