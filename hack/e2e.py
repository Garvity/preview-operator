#!/usr/bin/env python3
"""Real kind lifecycle tests. Uses its own kubeconfig and cleans up only its own cluster."""
import datetime as dt
import json
import os
from pathlib import Path
import socket
import subprocess
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
CLUSTER = os.environ.get("E2E_CLUSTER", "preview-operator-e2e")
IMAGE = os.environ.get("IMAGE", "preview-operator:dev")
NODE_IMAGE = os.environ.get("KIND_NODE_IMAGE", "kindest/node:v1.37.0")
APP_IMAGE = "hashicorp/http-echo:1.0.0"
KUBECTL = os.environ.get("KUBECTL", "kubectl")
NS = "preview-system"
EVIDENCE = ROOT / "docs" / "verification"
EVIDENCE.mkdir(parents=True, exist_ok=True)
CACHE = ROOT / ".cache" / "e2e"
CACHE.mkdir(parents=True, exist_ok=True)
ENV = dict(os.environ, KUBECONFIG=str(CACHE / "kubeconfig"))
report = {"startedAt": dt.datetime.now(dt.timezone.utc).isoformat(), "cluster": CLUSTER,
          "nodeImage": NODE_IMAGE, "operatorImage": IMAGE, "checks": [], "status": "running"}
logfile = (EVIDENCE / "e2e-run.log").open("w")


def log(message):
    print(message, flush=True)
    logfile.write(message + "\n")
    logfile.flush()


def run(*args, input=None, check=True, timeout=300):
    result = subprocess.run(args, cwd=ROOT, env=ENV, input=input, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if result.stdout:
        log(result.stdout.rstrip())
    if result.stderr:
        log(result.stderr.rstrip())
    if check and result.returncode:
        raise RuntimeError(f"{' '.join(args)} failed with exit {result.returncode}")
    return result


def kubectl(*args, **kwargs):
    return run(KUBECTL, "--request-timeout=20s", *args, **kwargs)


def obj(kind, name):
    result = kubectl("get", kind, name, "-n", NS, "-o", "json", "--ignore-not-found")
    return json.loads(result.stdout) if result.stdout.strip() else None


def wait_for(description, predicate, timeout=150):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            log(f"PASS: {description}")
            return
        time.sleep(2)
    raise TimeoutError(description)


def phase(name, expected):
    # Quiet reads keep polling logs concise; still fail on API errors.
    result = subprocess.run([KUBECTL, "--request-timeout=10s", "get", "preview", name,
                             "-n", NS, "-o", "json"], env=ENV, text=True, capture_output=True, timeout=15)
    if result.returncode:
        raise RuntimeError(result.stderr)
    return json.loads(result.stdout).get("status", {}).get("phase") == expected


def absent(kind, name):
    result = subprocess.run([KUBECTL, "--request-timeout=10s", "get", kind, name, "-n", NS,
                             "-o", "name", "--ignore-not-found"], env=ENV, text=True, capture_output=True, timeout=15)
    if result.returncode:
        raise RuntimeError(result.stderr)
    return not result.stdout.strip()


def apply_preview(name, ttl=3600):
    kubectl("apply", "-f", "-", input=json.dumps({
        "apiVersion": "preview.garv.dev/v1alpha1", "kind": "PreviewEnvironment",
        "metadata": {"name": name, "namespace": NS},
        "spec": {"image": APP_IMAGE, "args": ["-text=Hello from preview-operator", "-listen=:8080"],
                 "ttlSeconds": ttl, "replicas": 1, "containerPort": 8080, "servicePort": 80}}))


def record(name, function):
    start = time.monotonic()
    function()
    report["checks"].append({"name": name, "status": "passed", "seconds": round(time.monotonic()-start, 2)})


def port_forward_get(resource, remote_port, path="/"):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    proc = subprocess.Popen([KUBECTL, "-n", NS, "port-forward", resource, f"{port}:{remote_port}"],
                            env=ENV, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(40):
            if proc.poll() is not None:
                raise RuntimeError("port-forward exited unexpectedly")
            try:
                with urllib.request.urlopen(f"http://127.0.0.1:{port}{path}", timeout=2) as response:
                    return response.read().decode()
            except (OSError, urllib.error.URLError):
                time.sleep(0.5)
        raise TimeoutError("port-forward HTTP request")
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()


created = False
try:
    existing = run("kind", "get", "clusters").stdout.splitlines()
    if CLUSTER in existing:
        raise RuntimeError(f"Refusing to reuse existing cluster {CLUSTER}; set E2E_CLUSTER to a new name")
    run("docker", "build", "--build-arg", "VERSION=e2e", "--provenance=false", "--sbom=false", "-t", IMAGE, ".", timeout=600)
    # All further cleanup is scoped to this newly created, explicitly named cluster.
    created = True
    run("kind", "create", "cluster", "--name", CLUSTER, "--image", NODE_IMAGE,
        "--kubeconfig", ENV["KUBECONFIG"], "--wait", "120s", timeout=300)
    run("kind", "load", "docker-image", IMAGE, "--name", CLUSTER, timeout=300)
    manifests = run(KUBECTL, "kustomize", "config/default").stdout
    manifests = manifests.replace("image: preview-operator:dev", f"image: {IMAGE}")
    kubectl("apply", "-f", "-", input=manifests)
    kubectl("wait", "--for=condition=Established", "crd/previewenvironments.preview.garv.dev", "--timeout=60s")
    kubectl("rollout", "status", "deployment/preview-operator", "-n", NS, "--timeout=120s")
    report["kubernetes"] = json.loads(kubectl("version", "-o", "json").stdout)["serverVersion"]["gitVersion"]

    def provision():
        apply_preview("hello-preview")
        wait_for("preview Ready", lambda: phase("hello-preview", "Ready"))
        body = port_forward_get("service/hello-preview", 80)
        assert "Hello from preview-operator" in body, body
        report["httpResponse"] = body.strip()
        log(f"HTTP response: {body.strip()}")
    record("Provisioning and live HTTP response", provision)

    def self_heal():
        old = obj("deployment", "hello-preview")["metadata"]["uid"]
        kubectl("delete", "deployment/hello-preview", "service/hello-preview", "-n", NS, "--wait=true")
        wait_for("deleted children recreated and Ready", lambda: phase("hello-preview", "Ready") and
                 not absent("service", "hello-preview") and
                 (obj("deployment", "hello-preview") or {}).get("metadata", {}).get("uid") not in (old, None))
        assert "Hello from preview-operator" in port_forward_get("service/hello-preview", 80)
    record("Recovery after Deployment and Service deletion", self_heal)

    def scale():
        kubectl("patch", "preview/hello-preview", "-n", NS, "--type=merge", "-p", '{"spec":{"replicas":2}}')
        def scaled():
            p = obj("preview", "hello-preview")
            return p["status"].get("observedGeneration") == p["metadata"]["generation"] and p["status"].get("availableReplicas") == 2 and p["status"].get("phase") == "Ready"
        wait_for("scale to two replicas", scaled)
    record("Spec update and rollout readiness", scale)

    def collision():
        kubectl("create", "service", "clusterip", "collision", "--tcp=80:8080", "-n", NS)
        foreign_uid = obj("service", "collision")["metadata"]["uid"]
        apply_preview("collision")
        wait_for("ownership conflict reported", lambda: phase("collision", "Conflict"))
        s = obj("service", "collision")
        assert s["metadata"]["uid"] == foreign_uid and not s["metadata"].get("ownerReferences")
        assert absent("deployment", "collision")
        kubectl("delete", "preview/collision", "service/collision", "-n", NS)
    record("Foreign resource collision protection", collision)

    def rbac():
        identity = f"system:serviceaccount:{NS}:preview-operator"
        for resource, namespace in [("secrets", NS), ("deployments", "default")]:
            result = kubectl("auth", "can-i", "get", resource, "-n", namespace, "--as", identity, check=False)
            assert result.returncode == 1 and result.stdout.strip() == "no", result.stdout
        d = obj("deployment", "hello-preview")
        podspec = d["spec"]["template"]["spec"]
        assert podspec["automountServiceAccountToken"] is False
        assert podspec["securityContext"]["runAsNonRoot"] is True
        assert podspec["containers"][0]["securityContext"]["readOnlyRootFilesystem"] is True
    record("Namespace RBAC and workload security defaults", rbac)

    def metric_check():
        body = port_forward_get("deployment/preview-operator", 8080, "/metrics")
        assert 'preview_operator_phase_transitions_total{phase="Ready"}' in body
        assert "controller_runtime_reconcile_total" in body
        (EVIDENCE / "e2e-metrics.txt").write_text(body)
    record("Controller and lifecycle metrics", metric_check)

    def restart_expiry():
        apply_preview("short-lived", ttl=60)
        wait_for("short-lived preview Ready", lambda: phase("short-lived", "Ready"), timeout=45)
        p = obj("preview", "short-lived")
        expires = dt.datetime.fromisoformat(p["status"]["expiresAt"].replace("Z", "+00:00"))
        kubectl("scale", "deployment/preview-operator", "-n", NS, "--replicas=0")
        kubectl("wait", "--for=delete", "pod", "-l", "app.kubernetes.io/name=preview-operator", "-n", NS, "--timeout=60s")
        log("Operator stopped; waiting for the persisted deadline to pass")
        while dt.datetime.now(dt.timezone.utc) <= expires + dt.timedelta(seconds=2):
            time.sleep(1)
        kubectl("scale", "deployment/preview-operator", "-n", NS, "--replicas=1")
        kubectl("rollout", "status", "deployment/preview-operator", "-n", NS, "--timeout=120s")
        wait_for("expired preview cleaned up after manager restart", lambda: phase("short-lived", "Expired"))
        assert absent("deployment", "short-lived") and absent("service", "short-lived")
        kubectl("patch", "preview/short-lived", "-n", NS, "--type=merge", "-p", '{"spec":{"replicas":2}}')
        time.sleep(3)
        assert absent("deployment", "short-lived") and absent("service", "short-lived")
        report["expiredPreview"] = obj("preview", "short-lived")
    record("Expiry across operator downtime and no resurrection", restart_expiry)

    report["readyPreview"] = obj("preview", "hello-preview")
    (EVIDENCE / "e2e-resources.txt").write_text(kubectl("get", "preview,deploy,svc,pods", "-n", NS, "-o", "wide").stdout)

    def garbage_collection():
        p = obj("preview", "hello-preview")
        kubectl("delete", "preview/hello-preview", "-n", NS, "--wait=true")
        wait_for("owner deletion garbage collects children", lambda: absent("deployment", "hello-preview") and absent("service", "hello-preview"))
        kubectl("wait", "--for=delete", "pod", "-l", f"preview.garv.dev/environment-uid={p['metadata']['uid']}", "-n", NS, "--timeout=90s")
    record("Owner-reference garbage collection", garbage_collection)
    report["status"] = "passed"
except Exception as error:
    report["status"] = "failed"
    report["error"] = str(error)
    log(f"FAIL: {error}")
    if created:
        for args in [("get", "pods,deploy,preview", "-n", NS, "-o", "wide"),
                     ("get", "events", "-n", NS, "--sort-by=.lastTimestamp"),
                     ("logs", "deployment/preview-operator", "-n", NS, "--tail=100")]:
            try:
                kubectl(*args, check=False, timeout=30)
            except Exception as diagnostic_error:
                log(f"Diagnostic failed: {diagnostic_error}")
    raise
finally:
    if created and os.environ.get("KEEP_CLUSTER") != "1":
        try:
            run("kind", "delete", "cluster", "--name", CLUSTER, timeout=120)
            report["clusterCleanedUp"] = True
        except Exception as error:
            report["cleanupError"] = str(error)
            report["status"] = "failed"
    report["finishedAt"] = dt.datetime.now(dt.timezone.utc).isoformat()
    (EVIDENCE / "e2e-report.json").write_text(json.dumps(report, indent=2) + "\n")
    log(f"Final result: {report['status']}; evidence: docs/verification/e2e-report.json")
    logfile.close()
if report["status"] != "passed":
    raise SystemExit(1)
