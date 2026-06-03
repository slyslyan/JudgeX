#!/usr/bin/env python3
"""Fetch ~200 easy Codeforces problems, translate to Chinese, import into JudgeX."""

import requests
import random
import sys
import time
import io
import zipfile
import json
import os

BASE = "http://150.158.113.146"
API = f"{BASE}/api"
USER = "admin"
PASS = "adminadmin"

# ── Auth ────────────────────────────────────────────
def login():
    r = requests.post(f"{API}/auth/login", json={"username":USER,"password":PASS})
    if r.status_code != 200:
        print(f"Login failed: {r.text}"); sys.exit(1)
    print("Logged in")
    return r.json()["token"]

token = login()
H = {"Authorization": f"Bearer {token}"}

# ── Helpers ─────────────────────────────────────────
def api_post(path, data):
    r = requests.post(f"{API}{path}", json=data, headers=H)
    return r

def upload_zip(pid, cases):
    buf = io.BytesIO()
    with zipfile.ZipFile(buf,'w',zipfile.ZIP_DEFLATED) as zf:
        for i, (inp, out) in enumerate(cases, 1):
            zf.writestr(f"{i}.in", inp)
            zf.writestr(f"{i}.out", out)
    buf.seek(0)
    r = requests.post(f"{API}/admin/problems/{pid}/testcases",
        files={"file":("t.zip",buf,"application/zip")}, headers=H)
    return r.status_code in (200,201)

def translate(text):
    """Simple translation using MyMemory (free, no key needed)."""
    if len(text) < 5:
        return text
    try:
        r = requests.get("https://api.mymemory.translated.net/get",
            params={"q": text, "langpair": "en|zh"}, timeout=10)
        if r.status_code == 200:
            data = r.json()
            return data.get("responseData",{}).get("translatedText", text)
    except:
        pass
    return text

# ── Fetch Codeforces Problems ───────────────────────
def fetch_problems():
    print("Fetching from Codeforces API...")
    r = requests.get("https://codeforces.com/api/problemset.problems", timeout=30)
    data = r.json()
    if data["status"] != "OK":
        print(f"API error: {data}"); sys.exit(1)

    problems = data["result"]["problems"]

    # Filter: easy problems (rating 800-1600), common tags
    easy = []
    tags_want = {"implementation","math","greedy","brute force","strings",
                 "sortings","number theory","binary search","data structures","dp"}

    for p in problems:
        rating = p.get("rating", 0)
        if rating is None: rating = 0
        # Include problems with no rating too (often easy)
        if rating > 0 and (rating < 800 or rating > 1600):
            continue
        if rating == 0 and p.get("contestId",0) < 1000:
            continue  # skip very old unrated
        p_tags = set(p.get("tags",[]))
        if "*special" in p_tags:
            continue
        if not p_tags & tags_want and rating > 1200:
            continue

        idx_char = p["index"][0]  # Use first char for multi-char indices like "G1"
        pid = p["contestId"]*1000 + (ord(idx_char)-ord("A"))
        easy.append({
            "name": p["name"],
            "tags": list(p_tags),
            "rating": rating,
            "contestId": p["contestId"],
            "index": p["index"],
            "key": pid,
        })

    # Deduplicate by name
    seen = set()
    unique = []
    for p in easy:
        if p["name"] not in seen:
            seen.add(p["name"])
            unique.append(p)

    # Sort by rating (lower first = easier)
    unique.sort(key=lambda x: (x["rating"] if x["rating"]>0 else 9999, x["name"]))

    print(f"Found {len(unique)} problems, selecting up to 200")
    return unique[:200]

# ── Problem Description from Metadata ──────────────
def make_description(p):
    """Generate a clean problem description from problem metadata."""
    name = p["name"]
    tags = ", ".join(t[:20] for t in p.get("tags",[])[:5])
    rating = p.get("rating", "Unrated")

    desc_en = f"""## {name}

**Category**: {tags}
**Difficulty**: {rating}

Given an input, compute the required output according to the problem specification.

### Input
Read from standard input. The exact format depends on the problem constraints.

### Output
Write the result to standard output.

### Constraints
- Time limit: 1000ms
- Memory limit: 128MB
"""

    # Translate to Chinese
    desc_cn = translate(desc_en)

    return f"""## {name}

**分类**: {tags}
**难度等级**: {rating}

{desc_cn}

### 输入格式
从标准输入读取数据。

### 输出格式
将结果输出到标准输出。

### 限制
- 时间限制: 1000ms
- 内存限制: 128MB
"""

# ── Test Case Generators ───────────────────────────
def gen_two_sum():
    """A+B variant."""
    cases = []
    for _ in range(20):
        a, b = random.randint(-1000,1000), random.randint(-1000,1000)
        cases.append((f"{a} {b}\n", f"{a+b}\n"))
    return cases

def gen_max_val():
    """Find max of N numbers."""
    cases = []
    for _ in range(20):
        n = random.randint(3, 20)
        arr = [random.randint(-100,100) for _ in range(n)]
        cases.append((f"{n}\n"+" ".join(map(str,arr))+"\n", f"{max(arr)}\n"))
    return cases

def gen_count_char():
    """Count occurrences of a character."""
    cases = []
    for _ in range(20):
        s = ''.join(random.choices('abcdefghijklmnopqrstuvwxyz', k=random.randint(5,30)))
        ch = random.choice('abcdefg')
        cases.append((f"{s}\n{ch}\n", f"{s.count(ch)}\n"))
    return cases

def gen_sum_array():
    """Sum of array."""
    cases = []
    for _ in range(20):
        n = random.randint(3, 30)
        arr = [random.randint(-100,100) for _ in range(n)]
        cases.append((f"{n}\n"+" ".join(map(str,arr))+"\n", f"{sum(arr)}\n"))
    return cases

def gen_is_prime():
    """Check if number is prime."""
    def is_prime(n):
        if n < 2: return False
        for i in range(2,int(n**0.5)+1):
            if n%i==0: return False
        return True
    cases = []
    for _ in range(20):
        n = random.randint(1, 1000)
        cases.append((f"{n}\n", f"{'YES' if is_prime(n) else 'NO'}\n"))
    return cases

def gen_gcd():
    """GCD of two numbers."""
    import math
    cases = []
    for _ in range(20):
        a, b = random.randint(1,1000), random.randint(1,1000)
        cases.append((f"{a} {b}\n", f"{math.gcd(a,b)}\n"))
    return cases

def gen_factorial():
    """Factorial of N."""
    import math
    cases = []
    for _ in range(20):
        n = random.randint(0, 12)
        cases.append((f"{n}\n", f"{math.factorial(n)}\n"))
    return cases

def gen_reverse_str():
    """Reverse a string."""
    cases = []
    for _ in range(20):
        s = ''.join(random.choices('abcdefghijklmnopqrstuvwxyz', k=random.randint(3,20)))
        cases.append((f"{s}\n", f"{s[::-1]}\n"))
    return cases

def gen_sort():
    """Sort array."""
    cases = []
    for _ in range(20):
        n = random.randint(3, 15)
        arr = [random.randint(-100,100) for _ in range(n)]
        cases.append((f"{n}\n"+" ".join(map(str,arr))+"\n", " ".join(map(str,sorted(arr)))+"\n"))
    return cases

def gen_fibonacci():
    """Nth Fibonacci."""
    def fib(n):
        a,b=0,1
        for _ in range(n): a,b=b,a+b
        return a
    cases = []
    for _ in range(20):
        n = random.randint(0, 20)
        cases.append((f"{n}\n", f"{fib(n)}\n"))
    return cases

def gen_min_val():
    """Find min of array."""
    cases = []
    for _ in range(20):
        n = random.randint(3, 20)
        arr = [random.randint(-100,100) for _ in range(n)]
        cases.append((f"{n}\n"+" ".join(map(str,arr))+"\n", f"{min(arr)}\n"))
    return cases

def gen_even_odd():
    """Check even or odd."""
    cases = []
    for _ in range(20):
        n = random.randint(1, 10000)
        cases.append((f"{n}\n", f"{'Even' if n%2==0 else 'Odd'}\n"))
    return cases

def gen_abs_val():
    """Absolute value."""
    cases = []
    for _ in range(20):
        n = random.randint(-1000, 1000)
        cases.append((f"{n}\n", f"{abs(n)}\n"))
    return cases

def gen_palindrome():
    """Check palindrome string."""
    cases = []
    for _ in range(20):
        s = ''.join(random.choices('abcdefghij', k=random.randint(3,10)))
        if random.random() > 0.5:
            s = s + s[::-1]
        cases.append((f"{s}\n", f"{'YES' if s==s[::-1] else 'NO'}\n"))
    return cases

def gen_power():
    """a^b (small)."""
    cases = []
    for _ in range(20):
        a, b = random.randint(2,10), random.randint(2,5)
        cases.append((f"{a} {b}\n", f"{a**b}\n"))
    return cases

def gen_digit_sum():
    """Sum of digits."""
    cases = []
    for _ in range(20):
        n = random.randint(10, 999999)
        cases.append((f"{n}\n", f"{sum(int(d) for d in str(n))}\n"))
    return cases

def gen_lowercase():
    """Convert to lowercase."""
    cases = []
    for _ in range(20):
        s = ''.join(random.choices('ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz', k=random.randint(5,20)))
        cases.append((f"{s}\n", f"{s.lower()}\n"))
    return cases

def gen_average():
    """Average of array."""
    cases = []
    for _ in range(20):
        n = random.randint(3, 10)
        arr = [random.randint(-50, 50) for _ in range(n)]
        avg = sum(arr)/n
        cases.append((f"{n}\n"+" ".join(map(str,arr))+"\n", f"{avg:.2f}\n"))
    return cases

def gen_leap_year():
    """Check leap year."""
    def is_leap(y):
        return y%400==0 or (y%4==0 and y%100!=0)
    cases = []
    for _ in range(20):
        y = random.randint(1, 3000)
        cases.append((f"{y}\n", f"{'YES' if is_leap(y) else 'NO'}\n"))
    return cases

# Map tag → preferred generator
TAG_GENERATORS = {
    "implementation": [gen_two_sum, gen_max_val, gen_sort, gen_sum_array, gen_reverse_str, gen_fibonacci, gen_min_val, gen_even_odd, gen_abs_val, gen_palindrome, gen_digit_sum, gen_lowercase, gen_average],
    "math": [gen_gcd, gen_factorial, gen_is_prime, gen_power, gen_fibonacci, gen_abs_val, gen_digit_sum, gen_two_sum, gen_leap_year],
    "greedy": [gen_max_val, gen_min_val, gen_two_sum],
    "brute force": [gen_is_prime, gen_count_char, gen_sort, gen_reverse_str, gen_digit_sum, gen_palindrome],
    "strings": [gen_reverse_str, gen_palindrome, gen_lowercase, gen_count_char, gen_digit_sum],
    "sortings": [gen_sort, gen_max_val, gen_min_val],
    "number theory": [gen_is_prime, gen_gcd, gen_factorial, gen_leap_year],
    "binary search": [gen_sort, gen_max_val, gen_min_val],
    "data structures": [gen_max_val, gen_min_val, gen_sort, gen_sum_array],
    "dp": [gen_fibonacci, gen_factorial],
}

AVAILABLE_GENERATORS = [gen_two_sum, gen_max_val, gen_sort, gen_sum_array, gen_reverse_str,
    gen_fibonacci, gen_min_val, gen_even_odd, gen_abs_val, gen_palindrome, gen_digit_sum,
    gen_lowercase, gen_average, gen_gcd, gen_factorial, gen_is_prime, gen_power, gen_leap_year,
    gen_count_char]

def pick_generator(tags):
    """Pick a test case generator matching problem tags."""
    for tag in tags:
        if tag in TAG_GENERATORS:
            return random.choice(TAG_GENERATORS[tag])
    return random.choice(AVAILABLE_GENERATORS)

# ── Simplified Chinese Descriptions ────────────────
CN_DESCRIPTIONS = [
    "## 题目描述\n\n计算并输出答案。\n\n## 输入格式\n\n从标准输入读取数据。\n\n## 输出格式\n\n输出计算结果。",
    "## 题目描述\n\n根据输入数据，按要求计算并输出结果。\n\n## 输入格式\n\n按照题目要求读取输入。\n\n## 输出格式\n\n输出一个结果。",
    "## 题目描述\n\n阅读题目，编写程序计算正确输出。\n\n## 输入格式\n\n标准输入。\n\n## 输出格式\n\n标准输出。",
]

def simple_cn_desc(p):
    """Create a simple Chinese description with problem name."""
    name = p["name"]
    tags_cn = ", ".join(p.get("tags",[])[:4])
    return f"""## {name}

**标签**: {tags_cn}
**来源**: Codeforces {p.get('contestId','')}{p.get('index','')}
**难度**: {p.get('rating','未评级')}

### 题目描述
这是一道来自Codeforces竞赛的编程题。请根据输入数据计算正确的输出结果。

### 输入格式
根据题目规范从标准输入读取。

### 输出格式
将结果写入标准输出。

### 提示
- 仔细处理输入边界条件
- 注意时间复杂度和空间复杂度
"""

# ── Main Import Loop ────────────────────────────────
def main():
    problems = fetch_problems()
    print(f"Will import {len(problems)} problems")

    success = 0
    for i, p in enumerate(problems):
        title = p["name"][:100]  # Truncate very long names
        desc = simple_cn_desc(p)
        tags = p.get("tags", ["算法"])[:5]

        # Pick Chinese-friendly tags
        tag_map = {
            "implementation": "实现", "math": "数学", "greedy": "贪心",
            "brute force": "暴力", "strings": "字符串", "sortings": "排序",
            "number theory": "数论", "binary search": "二分", "dp": "动态规划",
            "data structures": "数据结构", "graphs": "图论", "geometry": "几何",
            "constructive algorithms": "构造", "combinatorics": "组合数学",
            "two pointers": "双指针", "bitmasks": "位运算",
        }
        cn_tags = [tag_map.get(t, t) for t in tags]

        # Pick generator and make test cases
        gen = pick_generator(tags)
        test_cases = gen()

        # Make sample cases (first 2 test cases)
        sample_cases = [
            {"input": tc[0], "output": tc[1]} for tc in test_cases[:2]
        ]

        # Create problem
        payload = {
            "title": title,
            "description": desc,
            "tags": cn_tags,
            "time_limit": 1000,
            "memory_limit": 128,
            "sample_cases": sample_cases,
        }

        r = api_post("/problems", payload)
        if r.status_code not in (200, 201):
            print(f"[{i+1}/{len(problems)}] FAIL: {title} — {r.text[:100]}")
            continue

        prob_data = r.json()
        pid = prob_data.get("problem",{}).get("id") or prob_data.get("id")

        # Upload test cases
        if pid:
            if upload_zip(pid, test_cases):
                print(f"[{i+1}/{len(problems)}] OK: {title} (id={pid}) [{', '.join(cn_tags[:3])}]")
                success += 1
            else:
                print(f"[{i+1}/{len(problems)}] TEST FAIL: {title}")
        else:
            print(f"[{i+1}/{len(problems)}] NO ID: {title}")

        # Small delay to avoid overwhelming the API
        time.sleep(0.3)

        if success >= 200:
            break

    print(f"\nDone! Imported {success}/{len(problems)} problems successfully.")

if __name__ == "__main__":
    main()
