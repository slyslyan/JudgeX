#!/usr/bin/env python3
"""
Fetch Codeforces problems and generate real descriptions using LLM.
Uses CF API for metadata + LLM for problem descriptions + test cases.
Imports directly into JudgeX.
"""

import requests
import json
import sys
import time
import io
import zipfile

BASE = "http://150.158.113.146"
API_BASE = f"{BASE}/api"
USER = "admin"
PASS = "adminadmin"

# ── Auth ────────────────────────────────────────────
def login():
    r = requests.post(f"{API_BASE}/auth/login", json={"username": USER, "password": PASS})
    if r.status_code != 200:
        print(f"Login failed: {r.text}"); sys.exit(1)
    print("Logged in as admin")
    return r.json()["token"]

token = login()
H = {"Authorization": f"Bearer {token}"}

# ── Helpers ─────────────────────────────────────────
def api_post(path, data):
    r = requests.post(f"{API_BASE}{path}", json=data, headers=H)
    return r

def upload_zip(pid, cases):
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, 'w', zipfile.ZIP_DEFLATED) as zf:
        for i, (inp, out) in enumerate(cases, 1):
            zf.writestr(f"{i}.in", inp)
            zf.writestr(f"{i}.out", out)
    buf.seek(0)
    r = requests.post(f"{API_BASE}/admin/problems/{pid}/testcases",
        files={"file": ("t.zip", buf, "application/zip")}, headers=H)
    return r.status_code in (200, 201)

# ── LLM for problem generation ──────────────────────
LLM_URL = "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
LLM_KEY = "sk-ecb67129c77c4befa0debac89e9dbc4b"  # From server config

def call_llm(system_prompt, user_prompt, max_tokens=2000):
    """Call Qwen-Plus via DashScope API."""
    r = requests.post(LLM_URL, json={
        "model": "qwen-plus",
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_prompt},
        ],
        "max_tokens": max_tokens,
        "temperature": 0.7,
    }, headers={
        "Authorization": f"Bearer {LLM_KEY}",
        "Content-Type": "application/json",
    }, timeout=60)
    if r.status_code != 200:
        print(f"  LLM error: {r.text[:200]}")
        return None
    return r.json()["choices"][0]["message"]["content"]

# ── Problem Generation ──────────────────────────────
SYSTEM_PROMPT = """You are a competitive programming problem writer. Given a Codeforces problem's metadata (title, tags, rating), generate a complete problem description in Chinese.

The description must include:
1. Problem background/context (story-like)
2. Detailed problem description
3. Input format specification
4. Output format specification
5. Constraints (time limit 1000ms, memory limit 256MB unless specified)
6. At least 2 example input/output pairs (with clear formatting)

Make the problem educational and interesting for Chinese OJ users.
IMPORTANT: Return ONLY a JSON object with these keys:
{
  "title_cn": "problem title in Chinese",
  "description": "full problem description in Chinese (markdown format)",
  "time_limit": 1000,
  "memory_limit": 256,
  "sample_cases": [{"input": "...", "output": "..."}, ...],
  "test_cases": [{"input": "...", "output": "..."}, ...]
}
Include at least 2 sample_cases and 10 test_cases. Test cases should cover edge cases."""

def generate_problem(meta):
    """Generate a full problem from CF metadata using LLM."""
    prompt = f"""Problem name: {meta['name']}
Problem tags: {', '.join(meta.get('tags', []))}
Difficulty rating: {meta.get('rating', 'Unknown')}
Codeforces ID: {meta.get('contestId')}{meta.get('index')}

Generate a complete Chinese problem description and test cases."""

    for attempt in range(3):
        resp = call_llm(SYSTEM_PROMPT, prompt, max_tokens=4000)
        if resp:
            # Try to extract JSON from response
            json_start = resp.find('{')
            json_end = resp.rfind('}') + 1
            if json_start >= 0 and json_end > json_start:
                try:
                    j = json.loads(resp[json_start:json_end])
                    # Validate required fields
                    if all(k in j for k in ['description', 'sample_cases', 'test_cases']):
                        return j
                except json.JSONDecodeError:
                    pass
        print(f"  LLM attempt {attempt+1} failed, retrying...")
        time.sleep(1)
    return None

# ── Main ────────────────────────────────────────────
TAG_MAP = {
    "implementation": "实现", "math": "数学", "greedy": "贪心",
    "brute force": "暴力", "strings": "字符串", "sortings": "排序",
    "number theory": "数论", "binary search": "二分", "dp": "动态规划",
    "data structures": "数据结构", "graphs": "图论", "geometry": "几何",
    "constructive algorithms": "构造", "combinatorics": "组合数学",
    "two pointers": "双指针", "bitmasks": "位运算",
    "dfs and similar": "DFS", "trees": "树", "shortest paths": "最短路",
}

def main():
    # Step 1: Fetch problems from Codeforces API
    print("Fetching problems from Codeforces API...")
    r = requests.get("https://codeforces.com/api/problemset.problems", timeout=30)
    data = r.json()
    if data["status"] != "OK":
        print(f"API error: {data}"); sys.exit(1)

    problems = data["result"]["problems"]
    tags_want = {"implementation", "math", "greedy", "brute force", "strings",
                 "sortings", "number theory", "binary search", "data structures", "dp"}

    candidates = []
    for p in problems:
        rating = p.get("rating", 0)
        if rating is None: rating = 0
        if rating > 0 and (rating < 800 or rating > 1600):
            continue
        if rating == 0 and p.get("contestId", 0) < 1000:
            continue
        p_tags = set(p.get("tags", []))
        if "*special" in p_tags:
            continue
        if not p_tags & tags_want and rating > 1200:
            continue
        candidates.append(p)

    # Deduplicate by name, prioritize rated problems
    seen = set()
    unique = []
    for p in candidates:
        if p["name"] not in seen:
            seen.add(p["name"])
            unique.append(p)

    unique.sort(key=lambda x: (x.get("rating", 0) if x.get("rating", 0) > 0 else 9999, x["name"]))
    print(f"Found {len(unique)} candidates, will import up to 20 with LLM-generated descriptions")
    print("(Limited to 20 to stay within API rate limits)\n")

    target = unique[:20]
    success = 0

    for i, p in enumerate(target):
        name = p["name"]
        contest_id = p["contestId"]
        index = p["index"]
        rating = p.get("rating", "Unrated")
        tags = p.get("tags", [])
        cn_tags = [TAG_MAP.get(t, t) for t in tags[:5]]

        print(f"[{i+1}/{len(target)}] Generating: {name} (CF {contest_id}{index}, rating={rating})")

        # Generate problem description and test cases via LLM
        gen = generate_problem({
            "name": name,
            "tags": tags,
            "rating": rating,
            "contestId": contest_id,
            "index": index,
        })

        if not gen:
            print(f"  SKIP: LLM generation failed")
            continue

        desc = gen.get("description", "")
        if not desc or len(desc) < 50:
            print(f"  SKIP: Description too short")
            continue

        # Build full description with metadata
        full_desc = f"""## {name}

**标签**: {', '.join(cn_tags)}
**来源**: Codeforces {contest_id}{index}
**难度**: {rating}

{desc}
"""
        title_cn = gen.get("title_cn", name)

        sample_cases = gen.get("sample_cases", [])
        test_cases = gen.get("test_cases", [])
        time_limit = gen.get("time_limit", 1000)
        memory_limit = gen.get("memory_limit", 256)

        if not sample_cases and test_cases:
            sample_cases = test_cases[:2]
        elif not test_cases and sample_cases:
            test_cases = sample_cases
        elif not sample_cases:
            print(f"  SKIP: No test cases generated")
            continue

        # Create problem
        payload = {
            "title": title_cn[:100],
            "description": full_desc,
            "tags": cn_tags,
            "time_limit": time_limit,
            "memory_limit": memory_limit,
            "sample_cases": sample_cases,
        }

        r = api_post("/problems", payload)
        if r.status_code not in (200, 201):
            print(f"  FAIL: {r.text[:200]}")
            continue

        prob_data = r.json()
        pid = prob_data.get("problem", {}).get("id") or prob_data.get("id")

        if pid:
            if upload_zip(pid, test_cases):
                print(f"  OK: id={pid}, '{title_cn}', test_cases={len(test_cases)}, samples={len(sample_cases)}")
                success += 1
            else:
                print(f"  TEST UPLOAD FAILED")
        else:
            print(f"  NO ID")

        # Rate limits
        time.sleep(2)

    print(f"\n{'='*50}")
    print(f"Imported {success}/{len(target)} problems with LLM-generated descriptions.")
    print(f"Visit: {BASE}/problems")

if __name__ == "__main__":
    main()
