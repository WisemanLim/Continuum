#!/usr/bin/env python3
"""Offline checks of the release / supply-chain configuration (FR-R03, R05, S08, X06).

Runs without goreleaser, cosign or network: YAML/JSON/plist/XML parse, cross references (build ids, files,
Dockerfiles), secrets only through ${{ secrets.* }}, no key material in the repository, workflow structure.
Usage: python3 scripts/release-lint.py   (exit 1 on the first class of problems found)
"""
import json
import pathlib
import plistlib
import re
import sys
import xml.etree.ElementTree as ET

import yaml

ROOT = pathlib.Path(__file__).resolve().parent.parent
problems = []
checks = 0


def check(cond, msg):
    global checks
    checks += 1
    if not cond:
        problems.append(msg)


def load_yaml(rel):
    p = ROOT / rel
    check(p.exists(), f"{rel}: missing")
    return yaml.safe_load(p.read_text()) if p.exists() else {}


# ---- goreleaser ----
g = load_yaml(".goreleaser.yaml")
check(g.get("version") == 2, ".goreleaser.yaml: version must be 2")
builds = {b["id"]: b for b in g.get("builds", [])}
check(set(builds) == {"continuum", "controller", "agent"}, f"builds: {sorted(builds)}")
for bid, b in builds.items():
    check((ROOT / b["main"]).is_dir(), f"build {bid}: main {b['main']} not found")
    check("-trimpath" in b.get("flags", []), f"build {bid}: -trimpath missing (reproducible builds)")
    check(any("-buildid=" in l for l in b.get("ldflags", [])), f"build {bid}: -buildid= missing")
    check(b.get("mod_timestamp") == "{{ .CommitTimestamp }}", f"build {bid}: mod_timestamp must be the commit time")
    check(b.get("env") == ["CGO_ENABLED=0"], f"build {bid}: CGO_ENABLED=0 expected")


def ids_ok(section, key="ids"):
    for item in g.get(section, []) or []:
        for i in item.get(key, []) or []:
            check(i in builds, f"{section}: unknown build id {i}")


for s in ("archives", "nfpms", "dockers_v2"):
    ids_ok(s)
for n in g.get("notarize", {}).get("macos", []):
    for i in n.get("ids", []):
        check(i in builds, f"notarize: unknown build id {i}")
    for k, v in {**n.get("sign", {}), **n.get("notarize", {})}.items():
        if k in ("certificate", "password", "issuer_id", "key_id", "key"):
            check(str(v).startswith("{{ .Env."), f"notarize.{k} must come from the CI environment")
for a in g.get("archives", []):
    for f in a.get("files", []):
        if "*" not in f:
            check((ROOT / f).exists() or f == "THIRD_PARTY_NOTICES.md", f"archives.files: {f} not found")
for d in g.get("dockers_v2", []):
    check((ROOT / d["dockerfile"]).exists(), f"dockers_v2: {d['dockerfile']} not found")
check(any(s.get("artifacts") == "checksum" and s.get("cmd") == "cosign" for s in g.get("signs", [])), "signs: cosign keyless signature of checksums missing")
check({s.get("artifacts") for s in g.get("sboms", [])} >= {"archive", "package"}, "sboms: archives and packages must get an SBOM")
check(any("tools/notices" in h for h in g.get("before", {}).get("hooks", [])), "before.hooks: THIRD_PARTY_NOTICES generation missing")

# ---- controller images serve the web UI (E37) ----
for df in ("Dockerfile", "deploy/release/Dockerfile.controller"):
    t = (ROOT / df).read_text()
    check("CONTINUUM_WEB_DIR=/web" in t and "/web" in t, f"{df}: web UI not copied to /web")
    check("USER 65532" in t or "nonroot" in t, f"{df}: controller image must run as non-root")
ctl = [d for d in g.get("dockers_v2", []) if d.get("id") == "controller"]
check(bool(ctl) and any(str(e.get("src")) == "apps/web/dist" for e in ctl[0].get("extra_files", [])), "dockers_v2 controller: apps/web/dist extra_files missing")

# ---- workflows ----
secret_ref = re.compile(r"\$\{\{\s*secrets\.([A-Z0-9_]+)\s*\}\}")
for wf in ("ci.yml", "release.yml"):
    rel = f".github/workflows/{wf}"
    w = load_yaml(rel)
    text = (ROOT / rel).read_text()
    jobs = w.get("jobs", {})
    check(bool(jobs), f"{rel}: no jobs")
    for jid, j in jobs.items():
        check("runs-on" in j, f"{rel}: job {jid} has no runs-on")
        for i, st in enumerate(j.get("steps", [])):
            check(("uses" in st) != ("run" in st), f"{rel}: {jid} step {i} needs exactly one of uses/run")
            run = st.get("run", "")
            check(not secret_ref.search(run), f"{rel}: {jid} step {i} interpolates a secret into a shell script (use env:)")
        for need in j.get("needs", []) if isinstance(j.get("needs"), list) else [j.get("needs")] if j.get("needs") else []:
            check(need in jobs, f"{rel}: {jid} needs unknown job {need}")
    if wf == "release.yml":
        check(w.get("permissions") == {"contents": "read"}, f"{rel}: default token permissions must be read-only")
        b = jobs.get("binaries", {})
        check(b.get("permissions", {}).get("id-token") == "write", f"{rel}: binaries needs id-token: write (keyless signing)")
        check("attest-build-provenance" in text, f"{rel}: build provenance attestation missing")
        check("goreleaser" in text and "args: check" in text, f"{rel}: goreleaser check step missing")
        check("tauri-action" in text and "wxt submit" in text, f"{rel}: desktop / extension publishing missing")
        bt = "\n".join(str(s.get("run", "")) for s in jobs.get("binaries", {}).get("steps", []))
        check("@continuum/web build" in bt, f"{rel}: binaries job must build the web UI before goreleaser")
        used = set(secret_ref.findall(text)) - {"GITHUB_TOKEN"}
        check(len(used) >= 15, f"{rel}: expected the signing / store secrets, got {sorted(used)}")
    else:
        matrix = jobs.get("go", {}).get("strategy", {}).get("matrix", {}).get("os", [])
        check(set(matrix) == {"ubuntu-22.04", "macos-14", "windows-2022"}, f"{rel}: FR-R05 matrix {matrix}")
        check("govulncheck" in text and "repro-check.sh" in text and "tools/notices" in text, f"{rel}: supply-chain job incomplete")
d = load_yaml(".github/dependabot.yml")
check({u["package-ecosystem"] for u in d.get("updates", [])} >= {"gomod", "npm", "cargo", "github-actions"}, "dependabot: ecosystems")

# ---- no key material committed ----
key_pat = re.compile(r"-----BEGIN (?:[A-Z ]*)PRIVATE KEY-----")
skip = {".git", "node_modules", "dist", "target", ".output", ".output-t3", "test", ".docs"}
for p in ROOT.rglob("*"):
    if any(part in skip for part in p.parts) or not p.is_file() or p.stat().st_size > 2_000_000:
        continue
    if p.suffix in {".p12", ".pfx", ".p8", ".jks", ".keystore", ".mobileprovision"}:
        check(False, f"key container committed: {p.relative_to(ROOT)}")
        continue
    try:
        if key_pat.search(p.read_text(errors="ignore")):
            check(p.name.endswith("_test.go"), f"private key in {p.relative_to(ROOT)}")
    except OSError:
        pass

# ---- extension enterprise policy ----
pol = ROOT / "deploy/extension-policy"
cj = json.loads((pol / "chrome-linux.json").read_text())
check(all(";" in e for e in cj["ExtensionInstallForcelist"]), "chrome-linux.json: forcelist entries need id;update_url")
check(cj["NativeMessagingAllowlist"] == ["com.continuum.agent"], "chrome-linux.json: native host allowlist")
mc = plistlib.loads((pol / "continuum-extension.mobileconfig").read_bytes())
check({p["PayloadType"] for p in mc["PayloadContent"]} == {"com.google.Chrome", "com.microsoft.Edge"}, "mobileconfig payloads")
check(len({p["PayloadUUID"] for p in mc["PayloadContent"]} | {mc["PayloadUUID"]}) == 3, "mobileconfig: payload UUIDs must be unique")
up = ET.parse(pol / "updates.xml").getroot()
check(up.tag.endswith("gupdate") and up.find("{http://www.google.com/update2/response}app") is not None, "updates.xml structure")
for reg in ("chrome-windows.reg", "edge-windows.reg"):
    t = (pol / reg).read_bytes()
    check(t.startswith(b"Windows Registry Editor Version 5.00\r\n"), f"{reg}: header / CRLF")
    check(b"ExtensionInstallForcelist" in t and b"EXTENSION_ID;" in t, f"{reg}: forcelist")

print(f"release-lint: {checks} checks, {len(problems)} problem(s)")
for p in problems:
    print("  ✗", p)
sys.exit(1 if problems else 0)
