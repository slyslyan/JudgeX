#!/usr/bin/env python3
"""JudgeX 压力测试 — 两阶段：并发提交 → 批量轮询结果"""

import argparse
import concurrent.futures
import random
import sys
import time
import urllib.request
import urllib.error
import json
import threading

BASE_URL = "http://150.158.113.146:8080/api"
USERS = 10
SUBMIT_PER_USER = 10

LANGUAGES = [
    ("c",   '#include <stdio.h>\nint main(){int a,b;scanf("%d%d",&a,&b);printf("%d\\n",a+b);return 0;}'),
    ("cpp", '#include <iostream>\nusing namespace std;int main(){int a,b;cin>>a>>b;cout<<a+b<<endl;return 0;}'),
    ("go",  'package main\nimport "fmt"\nfunc main(){var a,b int;fmt.Scan(&a,&b);fmt.Println(a+b)}'),
]

lock = threading.Lock()
all_submissions = []  # (username, lang, pid, sid, t_sub)


def api(method, path, token=None, body=None):
    url = f"{BASE_URL}{path}"
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    data = json.dumps(body).encode() if body else None
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    for attempt in range(3):
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                return json.loads(resp.read())
        except urllib.error.HTTPError as e:
            body = e.read().decode()[:200]
            if e.code == 409 or e.code == 429:
                time.sleep(1)
                continue
            return {"error": e.code, "body": body}
        except Exception as e:
            if attempt < 2:
                time.sleep(1)
                continue
            return {"error": str(e)}
    return {"error": "max retries"}


def register_user(idx):
    """Register one user, return (username, token) or (None, None)."""
    uid = f"load{idx:03d}"
    res = api("POST", "/auth/register", body={
        "username": uid, "email": f"{uid}@test.local",
        "password": "test1234", "confirm_password": "test1234"
    })
    if "token" in res:
        return uid, res["token"]
    # might already exist, try login
    res = api("POST", "/auth/login", body={"username": uid, "password": "test1234"})
    if "token" in res:
        return uid, res["token"]
    return None, None


def get_problem_ids(token):
    res = api("GET", "/problems?page_size=100", token=token)
    if "problems" in res:
        return [p["id"] for p in res["problems"]]
    return []


def submit_one(token, pid, lang, code):
    t0 = time.time()
    res = api("POST", "/submissions", token=token, body={
        "problem_id": pid, "language": lang, "code": code,
    })
    ms = int((time.time() - t0) * 1000)
    sid = res.get("submission_id")
    return sid, ms


def main():
    global BASE_URL, USERS, SUBMIT_PER_USER
    parser = argparse.ArgumentParser(description="JudgeX 压力测试")
    parser.add_argument("--users", type=int, default=USERS)
    parser.add_argument("--submits", type=int, default=SUBMIT_PER_USER)
    parser.add_argument("--url", default=BASE_URL)
    parser.add_argument("--poll-wait", type=int, default=60, help="最长等待判题结果秒数")
    args = parser.parse_args()

    BASE_URL = args.url
    USERS = args.users
    SUBMIT_PER_USER = args.submits
    total_subs = USERS * SUBMIT_PER_USER

    print(f"╔{' JudgeX 压力测试 ':=^58}╗")
    print(f"║  目标:      {BASE_URL:<45}║")
    print(f"║  配置:       {USERS}用户 × {SUBMIT_PER_USER}提交 = {total_subs} 请求     ║")
    print(f"╚{'':=^58}╝")

    # ── Phase 0: Register all users ──
    print("\n▶ Phase 0: 注册用户...")
    tokens = {}
    for i in range(1, USERS + 1):
        uid, token = register_user(i)
        if uid:
            tokens[uid] = token
    print(f"  ✓ {len(tokens)}/{USERS} 用户就绪")
    if not tokens:
        print("  ✗ 没有可用用户，退出")
        return

    # Get problem IDs from one user
    sample_token = next(iter(tokens.values()))
    all_pids = get_problem_ids(sample_token)
    if not all_pids:
        print("  ✗ 无法获取题目列表，退出")
        return
    print(f"  ✓ 获取到 {len(all_pids)} 道题目")

    # ── Phase 1: Concurrent Submit ──
    print(f"\n▶ Phase 1: 并发提交 {total_subs} 次...")
    t_start = time.time()
    usernames = list(tokens.keys())

    def submit_worker(username):
        token = tokens[username]
        pids = all_pids  # reuse cached list
        local = []
        for _ in range(SUBMIT_PER_USER):
            pid = random.choice(pids)
            lang, code = random.choice(LANGUAGES)
            sid, ms = submit_one(token, pid, lang, code)
            if sid:
                local.append((username, lang, pid, sid, time.time()))
            # brief jitter to avoid thundering herd
            time.sleep(random.uniform(0, 0.05))
        return local

    with concurrent.futures.ThreadPoolExecutor(max_workers=USERS) as pool:
        for result in pool.map(submit_worker, usernames):
            all_submissions.extend(result)

    submit_time = time.time() - t_start
    submitted = len(all_submissions)
    print(f"  ✓ {submitted}/{total_subs} 提交成功 ({submit_time:.1f}s, {submitted/submit_time:.0f} 提交/秒)")

    # ── Phase 2: Poll results ──
    print(f"\n▶ Phase 2: 等待判题结果 (最长 {args.poll_wait}s)...")
    t_poll_start = time.time()
    results = {}  # sid -> (username, lang, pid, t_sub, status, delay_ms, err)

    token_list = list(tokens.values())
    deadline = time.time() + args.poll_wait
    remaining = {s[3] for s in all_submissions}  # set of sids

    while remaining and time.time() < deadline:
        token = random.choice(token_list)
        # batch poll: check up to 30 submissions at once
        for sid in list(remaining)[:30]:
            res = api("GET", f"/submissions/{sid}", token=token)
            status = res.get("status", "")
            if status in ("Accepted", "Wrong Answer", "Compilation Error",
                          "Runtime Error", "Time Limit Exceeded",
                          "Memory Limit Exceeded"):
                # Find original submission data
                for username, lang, pid, s_sid, t_sub in all_submissions:
                    if s_sid == sid:
                        delay = int((time.time() - t_sub) * 1000)
                        err = res.get("error_message", "") or ""
                        results[sid] = (username, lang, pid, t_sub, status, delay, err)
                        break
                remaining.remove(sid)
        if remaining:
            # print(f"  ... 剩余 {len(remaining)} 个未出结果")
            time.sleep(2)

    # Any remaining are timed out
    for sid in remaining:
        for username, lang, pid, s_sid, t_sub in all_submissions:
            if s_sid == sid:
                delay = int((time.time() - t_sub) * 1000)
                results[sid] = (username, lang, pid, t_sub, "timeout", delay, "")
                break

    poll_time = time.time() - t_poll_start
    total_time = time.time() - t_start

    # ── Report ──
    ac = sum(1 for r in results.values() if r[4] == "Accepted")
    wa = sum(1 for r in results.values() if r[4] in ("Wrong Answer", "Compilation Error",
             "Runtime Error", "Time Limit Exceeded", "Memory Limit Exceeded"))
    to = sum(1 for r in results.values() if r[4] == "timeout")
    delays = [r[5] for r in results.values() if r[5] is not None]

    print(f"\n{' 测试报告 ':=^58}")
    print(f"  总耗时:        {total_time:.1f}s")
    print(f"  提交阶段:      {submit_time:.1f}s ({submitted/submit_time:.0f} 提交/秒)")
    print(f"  判题阶段:      {poll_time:.1f}s")
    print(f"  提交总数:      {submitted}")
    if ac:
        print(f"  通过 (AC):     {ac}")
    if wa:
        print(f"  未通过:        {wa}")
    if to:
        print(f"  超时:          {to}")
    if delays:
        delays.sort()
        print(f"  最小耗时:      {delays[0]}ms")
        print(f"  平均耗时:      {sum(delays)/len(delays):.0f}ms")
        print(f"  中位数:        {delays[len(delays)//2]}ms")
        print(f"  P95:           {delays[int(len(delays)*0.95)]}ms")
        print(f"  P99:           {delays[int(len(delays)*0.99)]}ms")
        print(f"  最大耗时:      {delays[-1]}ms")
    print("=" * 60)

    # Print AC submissions in detail
    if ac:
        print(f"\n 通过详情 ({ac}):")
        for r in sorted(results.values(), key=lambda x: x[5]):
            if r[4] == "Accepted":
                print(f"  ✓ {r[0]} pid={r[2]} {r[1]:6s} → {r[4]:15s} {r[5]}ms")


if __name__ == "__main__":
    main()
