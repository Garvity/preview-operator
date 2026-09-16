# Feature audit — September 16, 2026

The operator was rebuilt and executed in an isolated Kubernetes 1.37 kind cluster.
The final run started at **2026-09-16T12:52:21.791097+00:00** and completed at **2026-09-16T12:59:35.214113+00:00**.
All **18 live feature checks** passed. The test cluster was removed after the run.

## Bug found and fixed

An API-server regression test showed that manually setting a managed Deployment's
`spec.paused` or `spec.minReadySeconds` was not repaired. A paused rollout could
prevent subsequent PreviewEnvironment changes from taking effect. Reconciliation
now explicitly restores `paused: false` and `minReadySeconds: 0`. Pause/resume
remains available through the supported CR field, `spec.replicas: 0`.

The regression was observed failing before the fix (`regression-before-fix.log`)
and passing afterward (`integration-tests.log`). It is
covered by unit tests, a real API-server regression, and the live drift check.
A second added regression confirmed that limits-only resource budgets remain
idempotent under API-server defaulting; that case did not require a code change.

## Live verification

| Feature | Result | Duration |
| --- | --- | --- |
| Provisioning and live HTTP response | PASS | 11.23s |
| Recovery after Deployment and Service deletion | PASS | 2.98s |
| Spec update and rollout readiness | PASS | 2.24s |
| Foreign resource collision protection | PASS | 0.44s |
| Namespace RBAC and workload security defaults | PASS | 0.17s |
| Controller and lifecycle metrics | PASS | 0.53s |
| Server defaults and invalid API input rejection | PASS | 0.50s |
| Pause and resume preserve the deadline | PASS | 2.61s |
| Custom arguments ports and resource budgets | PASS | 5.25s |
| Deployment and Service drift repair | PASS | 2.99s |
| Invalid workload budget error and recovery | PASS | 0.30s |
| Image rollout failure and recovery | PASS | 124.19s |
| Health readiness Events and stable conditions | PASS | 3.41s |
| Out-of-namespace previews remain untouched | PASS | 3.28s |
| Leader election and failover | PASS | 8.04s |
| Online expiry and blocked-cleanup recovery | PASS | 61.62s |
| Expiry across operator downtime and no resurrection | PASS | 72.06s |
| Owner-reference garbage collection | PASS | 1.59s |

The failed-image check waited for the real 120-second Deployment progress deadline.
Online expiry held a test-owned finalizer, verified `Expiring` rather than premature
`Expired`, then released only that finalizer and observed completion. Failover ran
two manager replicas, deleted the lease holder, observed a different holder, and
verified the replacement leader repaired a deleted Service. Namespace isolation
was verified with a real CR outside the watched namespace in addition to RBAC checks.

Intentional invalid inputs, the invalid image and budget, and deliberate Deployment
drift generated expected warnings/errors in the transcript. These are negative
checks, not unexplained failures. All affected previews recovered as asserted.

## Other validation

- Unit/race tests: passed; controller coverage **87.1%**.
- API coverage including generated deepcopy: **39.4%**; combined unit coverage: **74.6%**.
- `go vet`, static manager build and generated-file check: passed.
- Real API-server integration, including both new regressions: passed.
- CLI version, help and required-namespace failure behavior: passed.
- Deployment manifests rendered and applied successfully to the live cluster.

Evidence: `build-tests.log`, `integration-tests.log`, `coverage-summary.txt`,
`cli-checks.json`, `e2e-report.json` and `e2e-run.log` in this directory. All images
are under `output-images/`, including the refreshed `final-output.png` and SVG.
These results cover the documented v1alpha1 feature set and exercised failure cases;
they do not establish compatibility with every Kubernetes version, image or workload,
nor replace load testing, a security audit or a cloud infrastructure HA exercise.
