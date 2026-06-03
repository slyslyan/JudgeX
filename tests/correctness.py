#!/usr/bin/env python3
"""JudgeX Correctness Test Suite"""

import sys, json, time, http.client, urllib.parse

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080"
p = urllib.parse.urlparse(BASE)
HOST = p.hostname
PORT = p.port or 80

PASS = 0
FAIL = 0

GREEN = "\033[0;32m"
RED = "\033[0;31m"
NC = "\033[0m"

def ok(msg):
    global PASS
    PASS += 1
    print(f"{GREEN}PASS{NC} {msg}")

def fail(msg, detail=""):
    global FAIL
    FAIL += 1
    print(f"{RED}FAIL{NC} {msg} — {detail}" if detail else f"{RED}FAIL{NC} {msg}")

def api(method, path, data=None, token=None):
    conn = http.client.HTTPConnection(HOST, PORT, timeout=15)
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    body = json.dumps(data) if data else None
    conn.request(method, path, body=body, headers=headers)
    resp = conn.getresponse()
    raw = resp.read().decode()
    conn.close()
    try:
        return resp.status, json.loads(raw), raw
    except json.JSONDecodeError:
        return resp.status, {}, raw

print("═══ JudgeX Correctness Tests ═══")
print(f"Target: {BASE}")
print()

# ── Auth ──
print("── Auth ──")

status, body, _ = api("POST", "/api/auth/register", {
    "username": "pycorrect", "password": "test123",
    "email": "py@test.local", "confirm_password": "test123"
})
if status in (200, 201): ok("Register")
else: fail("Register", f"HTTP {status}")

status, body, _ = api("POST", "/api/auth/login",
    {"username": "pycorrect", "password": "test123"})
TOKEN = body.get("token", "")
if TOKEN: ok("Login")
else: fail("Login", "no token")

status, body, _ = api("POST", "/api/auth/login",
    {"username": "pycorrect", "password": "wrong"})
if status == 401: ok("Bad password blocked")
else: fail("Bad password blocked", f"HTTP {status}")

# ── Problems ──
print()
print("── Problems ──")

status, body, _ = api("GET", "/api/problems")
if status == 200: ok(f"List problems ({body.get('total', 0)} found)")
else: fail("List problems", f"HTTP {status}")

status, body, _ = api("GET", "/api/problems/1")
if status == 200: ok("Get problem #1")
else: fail("Get problem #1", f"HTTP {status}")

status, _, _ = api("GET", "/api/problems/99999")
if status == 404: ok("Missing problem = 404")
else: fail("Missing problem = 404", f"HTTP {status}")

# ── Submissions ──
print()
print("── Submissions ──")

def submit_and_wait(code, language, expected_verdict, name):
    status, body, raw = api("POST", "/api/submissions", {
        "problem_id": 1, "language": language, "code": code
    }, token=TOKEN)
    sub_id = body.get("submission_id", 0)
    if sub_id > 0:
        ok(f"Submit {name} (id={sub_id})")
    else:
        fail(f"Submit {name}", f"no ID, body={raw[:100]}")

    verdict = "pending"
    for _ in range(60):
        time.sleep(0.5)
        status, body, _ = api("GET", f"/api/submissions/{sub_id}", token=TOKEN)
        verdict = body.get("status", "pending")
        if verdict not in ("pending", "judging"):
            break

    if verdict == expected_verdict:
        ok(f"{name} → {verdict}")
    else:
        fail(f"{name}", f"expected {expected_verdict}, got {verdict}")

submit_and_wait(
    "#include <iostream>\nint main() { int a, b; std::cin >> a >> b; std::cout << a + b << std::endl; return 0; }",
    "cpp", "Accepted", "C++ AC")

submit_and_wait(
    "#include <iostream>\nint main() { std::cout << \"wrong\" << std::endl; return 0; }",
    "cpp", "Wrong Answer", "C++ WA")

submit_and_wait(
    "broken c++ syntax {{{",
    "cpp", "Compile Error", "C++ CE")

submit_and_wait(
    "import sys\na, b = map(int, sys.stdin.read().split())\nprint(a + b)",
    "python", "Accepted", "Python AC")

# ── Profile & Leaderboard ──
print()
print("── Profile & Leaderboard ──")

status, body, _ = api("GET", "/api/profile", token=TOKEN)
if status == 200: ok("Get profile")
else: fail("Get profile", f"HTTP {status}")

status, body, _ = api("GET", "/api/leaderboard")
if status == 200: ok("Leaderboard")
else: fail("Leaderboard", f"HTTP {status}")

# ── Auth Guard ──
print()
print("── Auth Guard ──")

status, _, _ = api("POST", "/api/submissions",
    {"problem_id": 1, "language": "cpp", "code": "int main(){}"})
if status == 401: ok("Submit w/o auth = 401")
else: fail("Submit w/o auth = 401", f"HTTP {status}")

status, _, _ = api("POST", "/api/problems", {"title": "hack"}, token=TOKEN)
if status == 403: ok("Create problem w/o admin = 403")
else: fail("Create problem w/o admin = 403", f"HTTP {status}")

# ── Health ──
print()
print("── Health ──")

status, body, _ = api("GET", "/health")
if status == 200: ok("Liveness /health")
else: fail("Liveness /health", f"HTTP {status}")

status, body, _ = api("GET", "/ready")
if status == 200: ok("Readiness /ready")
else: fail("Readiness /ready", f"HTTP {status}")

# ── Submissions List ──
print()
print("── Submissions List ──")

status, body, _ = api("GET", "/api/submissions", token=TOKEN)
if status == 200:
    count = body.get("total", len(body.get("submissions", [])))
    ok(f"List submissions ({count} found)")
else:
    fail("List submissions", f"HTTP {status}")

# "My submissions" filter
status, body, _ = api("GET", "/api/submissions?mine=true", token=TOKEN)
if status == 200:
    subs = body.get("submissions", [])
    ok(f"My submissions filter ({len(subs)} found)")
else:
    fail("My submissions filter", f"HTTP {status}")

# ── Summary ──
print()
print("═══════════════════════════════════════")
total = PASS + FAIL
print(f"  Passed: {GREEN}{PASS}{NC} / {total}")
if FAIL > 0:
    print(f"  Failed: {RED}{FAIL}{NC} / {total}")
print("═══════════════════════════════════════")
sys.exit(FAIL)
