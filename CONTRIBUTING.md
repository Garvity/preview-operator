# Contributing

Start with a reproducible problem and a small change. Describe expected behavior,
actual behavior, Kubernetes/Go versions and a minimal CR. Do not include secrets,
private kubeconfigs or credentials in issues, test fixtures or screenshots.

## Development checks

```bash
make fmt
make verify
make envtest test-integration
make test-e2e
```

When changing API markers, run `make generate` and include the generated CRD and
deepcopy changes. The generated-file check expects those files to match the index/
committed baseline. Do not manually edit generated files. Keep dependencies pinned
and review `go.mod` / `go.sum` changes.

Use table-driven unit tests for lifecycle/error paths, envtest for API behavior,
and kind for kubelet/controller-manager/garbage-collector behavior. envtest does
not run workloads or garbage collection. A new feature should test an observable
contract or failure mode, not just repeat its implementation.

Run `make build VERSION=your-version` for a local binary. `make run` starts the
manager against your selected kubeconfig and the existing `preview-system`
namespace, with leader election disabled for local iteration.

Commit each cohesive milestone with a descriptive message. Include the problem,
behavioral change, validation and material limitations in pull requests. The
initial repository's author/committer times were explicitly assigned within the
user-requested September 14–15, 2026 window; they are not a record of elapsed work.
Verification artifacts retain their actual execution timestamps.

Place all generated visual artifacts under `output-images/`. The initial visual
report is generated from measured evidence; do not label a mockup as a screenshot
or present skipped tests as passed. Large raw diagnostics can remain CI artifacts.
