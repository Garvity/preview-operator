# Changelog

## Unreleased — September 16, 2026

- Fixed reconciliation of manual Deployment rollout pauses and readiness delays.
- Expanded live acceptance coverage from 8 to 18 feature checks, including custom
  configuration, negative cases, leader failover and blocked expiry cleanup.
- Added API-server regressions and refreshed measured evidence and output images.

## 0.1.0 — Initial local release

- Added the PreviewEnvironment v1alpha1 CRD with validation, defaults and immutable TTL.
- Added ownership-safe reconciliation, rollout status, drift repair and expiry cleanup.
- Added the namespace-scoped manager, hardened container deployment and metrics.
- Added unit/race tests, API-server integration and isolated kind lifecycle checks.
- Added implementation plan, architecture, runbook and reproducible verification evidence.

This entry describes the local implementation; it does not imply a GitHub release
or published container image.
