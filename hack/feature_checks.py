"""Additional live acceptance checks for the operator's documented feature surface."""
import datetime as dt
import json
import time


def run_feature_checks(*, kubectl, obj, wait_for, phase, port_forward_get,
                       record, apply_preview, absent, log, report, namespace, app_image):
    ns, name = namespace, "hello-preview"

    def patch(kind, resource, spec):
        kubectl("patch", f"{kind}/{resource}", "-n", ns, "--type=merge", "-p", json.dumps({"spec": spec}))

    def current_ready():
        p = obj("preview", name)
        status = p.get("status", {})
        return status.get("phase") == "Ready" and status.get("observedGeneration") == p["metadata"]["generation"]

    def defaults_validation():
        resource = {"apiVersion": "preview.garv.dev/v1alpha1", "kind": "PreviewEnvironment",
                    "metadata": {"name": "defaults-check", "namespace": ns}, "spec": {"image": app_image}}
        kubectl("create", "-f", "-", input=json.dumps(resource))
        p = obj("preview", "defaults-check")
        assert {key: p["spec"][key] for key in ("replicas", "containerPort", "servicePort", "ttlSeconds")} == {
            "replicas": 1, "containerPort": 8080, "servicePort": 80, "ttlSeconds": 3600}
        for changes in ({"ttlSeconds": 7200}, {"replicas": -1}, {"containerPort": 80}, {"servicePort": 65536}, {"args": ["x"] * 33}, {"image": "bad image"}):
            result = kubectl("patch", "preview/defaults-check", "-n", ns, "--type=merge", "-p", json.dumps({"spec": changes}), check=False)
            assert result.returncode != 0 and "invalid" in result.stderr.lower(), result.stderr
        kubectl("delete", "preview/defaults-check", "-n", ns)
    record("Server defaults and invalid API input rejection", defaults_validation)

    def pause_resume():
        deadline = obj("preview", name)["status"]["expiresAt"]
        patch("preview", name, {"replicas": 0})
        wait_for("preview paused", lambda: phase(name, "Paused"))
        wait_for("paused Deployment reaches zero replicas", lambda: obj("deployment", name).get("status", {}).get("replicas", 0) == 0)
        p = obj("preview", name)
        assert not p["status"].get("endpoint") and p["status"]["expiresAt"] == deadline
        patch("preview", name, {"replicas": 2})
        wait_for("preview resumes", current_ready)
        assert obj("preview", name)["status"]["expiresAt"] == deadline
    record("Pause and resume preserve the deadline", pause_resume)

    def configuration():
        ip = obj("service", name)["spec"]["clusterIP"]
        budget = {"requests": {"cpu": "60m", "memory": "40Mi"}, "limits": {"cpu": "300m", "memory": "160Mi"}}
        patch("preview", name, {"args": ["-text=Custom configuration verified", "-listen=:9091"],
                                "containerPort": 9091, "servicePort": 8088, "resources": budget})
        wait_for("custom configuration rolls out", current_ready)
        p, d, s = obj("preview", name), obj("deployment", name), obj("service", name)
        assert s["spec"]["clusterIP"] == ip and s["spec"]["ports"][0]["port"] == 8088
        assert d["spec"]["template"]["spec"]["containers"][0]["resources"] == budget
        assert p["status"]["endpoint"].endswith(":8088")
        assert "Custom configuration verified" in port_forward_get(f"service/{name}", 8088)
        patch("preview", name, {"args": ["-text=Hello from preview-operator", "-listen=:8080"], "containerPort": 8080, "servicePort": 80, "resources": None})
        wait_for("default configuration restored", current_ready)
    record("Custom arguments ports and resource budgets", configuration)

    def drift():
        patch("deployment", name, {"replicas": 0, "paused": True, "minReadySeconds": 30,
                                    "template": {"spec": {"containers": [{"name": "app", "image": app_image, "args": ["-text=drift", "-listen=:8080"]}]}}})
        patch("service", name, {"selector": {"preview.garv.dev/environment-uid": "wrong"}, "ports": [{"name": "http", "port": 8089, "targetPort": "http"}]})
        def repaired():
            d, s = obj("deployment", name), obj("service", name)
            return (not d["spec"].get("paused", False) and d["spec"].get("minReadySeconds", 0) == 0
                    and d["spec"]["replicas"] == 2 and s["spec"]["ports"][0]["port"] == 80
                    and s["spec"]["selector"].get("preview.garv.dev/environment-uid") == obj("preview", name)["metadata"]["uid"]
                    and d["spec"]["template"]["spec"]["containers"][0]["args"][0] == "-text=Hello from preview-operator")
        wait_for("all managed drift repaired", repaired)
        wait_for("repaired rollout Ready", current_ready)
        assert "Hello from preview-operator" in port_forward_get(f"service/{name}", 80)
    record("Deployment and Service drift repair", drift)

    def budget_failure():
        patch("preview", name, {"resources": {"requests": {"cpu": "2"}, "limits": {"cpu": "100m"}}})
        wait_for("invalid child budget reported as Error", lambda: phase(name, "Error"))
        p = obj("preview", name)
        ready = next(c for c in p["status"]["conditions"] if c["type"] == "Ready")
        assert ready["reason"] == "ReconcileFailed" and ready["status"] == "False"
        patch("preview", name, {"resources": None})
        wait_for("corrected budget recovers", current_ready)
    record("Invalid workload budget error and recovery", budget_failure)

    def rollout_failure():
        patch("preview", name, {"image": "invalid.invalid/preview-operator/nonexistent:missing"})
        wait_for("failed image rollout is not Ready", lambda: phase(name, "Pending"))
        assert not obj("preview", name)["status"].get("endpoint")
        log("Waiting for the real Deployment progress deadline (120 seconds)")
        wait_for("rollout failure reported", lambda: phase(name, "Error"), timeout=210)
        p = obj("preview", name)
        ready = next(c for c in p["status"]["conditions"] if c["type"] == "Ready")
        assert ready["reason"] == "RolloutFailed" and ready["observedGeneration"] == p["metadata"]["generation"]
        patch("preview", name, {"image": app_image})
        wait_for("corrected image recovers", current_ready)
    record("Image rollout failure and recovery", rollout_failure)

    def probes_events():
        for path in ("/healthz", "/readyz"):
            assert port_forward_get("deployment/preview-operator", 8081, path).strip() == "ok"
        p = obj("preview", name)
        events = json.loads(kubectl("get", "events", "-n", ns, "--field-selector", f"involvedObject.uid={p['metadata']['uid']}", "-o", "json").stdout)["items"]
        reasons = {e["reason"] for e in events}
        assert {"DeploymentAvailable", "RolloutFailed", "ReconcileFailed"} <= reasons, reasons
        before = p["status"]["conditions"]
        # Trigger another reconciliation without changing the requested state.
        kubectl("annotate", f"preview/{name}", "-n", ns, "audit.garv.dev/check=stable", "--overwrite")
        time.sleep(2)
        assert obj("preview", name)["status"]["conditions"] == before
    record("Health readiness Events and stable conditions", probes_events)

    def isolation():
        kubectl("create", "-f", "-", input=json.dumps({"apiVersion": "preview.garv.dev/v1alpha1", "kind": "PreviewEnvironment",
                "metadata": {"name": "outside-watch", "namespace": "default"}, "spec": {"image": app_image}}))
        time.sleep(3)
        p = json.loads(kubectl("get", "preview/outside-watch", "-n", "default", "-o", "json").stdout)
        assert not p.get("status")
        result = kubectl("get", "deployment/outside-watch", "service/outside-watch", "-n", "default", "--ignore-not-found", "-o", "name")
        assert not result.stdout.strip()
        kubectl("delete", "preview/outside-watch", "-n", "default")
    record("Out-of-namespace previews remain untouched", isolation)

    def leader_failover():
        lease = "preview-operator.preview.garv.dev"
        patch("deployment", "preview-operator", {"replicas": 2})
        kubectl("rollout", "status", "deployment/preview-operator", "-n", ns, "--timeout=90s")
        original = obj("lease", lease)["spec"]["holderIdentity"]
        pods = json.loads(kubectl("get", "pods", "-n", ns, "-l", "app.kubernetes.io/name=preview-operator", "-o", "json").stdout)["items"]
        leader = next(p["metadata"]["name"] for p in pods if original.startswith(p["metadata"]["name"]+"_"))
        kubectl("delete", "pod", leader, "-n", ns, "--wait=true")
        wait_for("standby takes leader lease", lambda: obj("lease", lease)["spec"].get("holderIdentity") not in (None, "", original))
        kubectl("delete", f"service/{name}", "-n", ns)
        wait_for("new leader repairs Service", lambda: not absent("service", name))
        assert "Hello from preview-operator" in port_forward_get(f"service/{name}", 80)
        patch("deployment", "preview-operator", {"replicas": 1})
        kubectl("rollout", "status", "deployment/preview-operator", "-n", ns, "--timeout=90s")
        report["leaderFailoverVerified"] = True
    record("Leader election and failover", leader_failover)

    def online_expiry():
        apply_preview("held-expiry", ttl=60)
        wait_for("held preview Ready", lambda: phase("held-expiry", "Ready"), timeout=45)
        kubectl("patch", "deployment/held-expiry", "-n", ns, "--type=merge", "-p", '{"metadata":{"finalizers":["audit.garv.dev/hold"]}}')
        wait_for("online deadline starts cleanup", lambda: phase("held-expiry", "Expiring"), timeout=90)
        p = obj("preview", "held-expiry")
        assert not p["status"].get("endpoint")
        assert next(c for c in p["status"]["conditions"] if c["type"] == "Expired")["status"] == "True"
        assert obj("deployment", "held-expiry")["metadata"].get("deletionTimestamp")
        # Release only the finalizer created by this test; do not touch unrelated finalizers.
        current = obj("deployment", "held-expiry")["metadata"]["finalizers"]
        kubectl("patch", "deployment/held-expiry", "-n", ns, "--type=json", "-p", json.dumps([
            {"op": "test", "path": "/metadata/finalizers", "value": current},
            {"op": "replace", "path": "/metadata/finalizers", "value": [f for f in current if f != "audit.garv.dev/hold"]}]))
        wait_for("cleanup completes after releasing test finalizer", lambda: phase("held-expiry", "Expired"))
        assert absent("deployment", "held-expiry") and absent("service", "held-expiry")
        kubectl("delete", "preview/held-expiry", "-n", ns)
    record("Online expiry and blocked-cleanup recovery", online_expiry)
