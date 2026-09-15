# Security model

Preview Operator is for trusted application authors sharing a team namespace.
Authors choose executable container images. The operator does not validate image
provenance, inspect application code or enforce network isolation. Platform owners
must decide who can create PreviewEnvironment CRs and configure admission, quotas,
network policy and image policy for their environment.

The manager cannot read Secrets or workloads in other namespaces under the shipped
Role. It can create and modify Deployments/Services, emit Events, update preview
status and participate in leader election. Delete permission on the owner CR is
included for Kubernetes ownerReference blockOwnerDeletion admission. A compromised
manager can exercise all its namespace permissions.

Application containers have no mounted service-account token, use a fixed non-root
UID, drop all capabilities and use RuntimeDefault seccomp. Their root filesystem is
read-only with a bounded `/tmp` volume. These defaults reduce accidental privilege;
they do not make arbitrary workloads safe.

Report suspected vulnerabilities privately to the repository owner using GitHub
private vulnerability reporting when enabled. Until a remote repository and private
reporting channel exist, contact the owner directly through an established private
channel; do not publish exploit details or secrets in a public issue. There is no
formal security response SLA for this initial project.
