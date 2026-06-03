package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	baseURL   = flag.String("url", "http://localhost:8080", "backend base URL")
	users     = flag.Int("users", 20, "number of concurrent users")
	subs      = flag.Int("subs", 100, "total submissions to send")
	concur    = flag.Int("concur", 10, "concurrent submission workers")
	problemID = flag.Int("problem", 1, "problem ID to submit to")
	duration  = flag.Duration("dur", 0, "duration mode (e.g. 30s), overrides --subs")
	apiConcur = flag.Int("api-concur", 5, "API load test concurrency")
	skipAPI   = flag.Bool("skip-api", false, "skip API load test")
	skipSub   = flag.Bool("skip-sub", false, "skip submission stress test")
)

type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	Token    string `json:"-"`
	client   *http.Client
}

type LoginResp struct {
	Token string `json:"token"`
}

type SubmissionReq struct {
	ProblemID int    `json:"problem_id"`
	Language  string `json:"language"`
	Code      string `json:"code"`
}

type Stats struct {
	TotalSubmitted  atomic.Int64
	TotalCompleted  atomic.Int64
	Accepted        atomic.Int64
	WrongAnswer     atomic.Int64
	TLE             atomic.Int64
	MLE             atomic.Int64
	RuntimeError    atomic.Int64
	CompileError    atomic.Int64
	Pending         atomic.Int64
	Errors          atomic.Int64
	TotalLatencyMs  atomic.Int64
	APISuccess      atomic.Int64
	APIFail         atomic.Int64
	APITotalLatency atomic.Int64
	latencyBuckets  map[string]*atomic.Int64
	mu              sync.Mutex
}

func main() {
	flag.Parse()
	fmt.Println("═══ JudgeX Stress Test ═══")
	fmt.Printf("Target: %s | Users: %d | Submissions: %d | Workers: %d\n",
		*baseURL, *users, *subs, *concur)
	fmt.Println()

	stats := &Stats{
		latencyBuckets: map[string]*atomic.Int64{
			"<100ms": {},
			"<500ms": {},
			"<1s":    {},
			"<3s":    {},
			"<10s":   {},
			">=10s":  {},
		},
	}

	// Phase 1: Register users
	fmt.Println("── Phase 1: Registering users ──")
	usersList := registerUsers(*users)
	fmt.Printf("  Registered %d users\n", len(usersList))

	// Phase 2: Submission stress test
	if !*skipSub && len(usersList) > 0 {
		fmt.Println()
		fmt.Println("── Phase 2: Submission stress test ──")
		runSubmissionStress(usersList, stats)
	}

	// Phase 3: API load test
	if !*skipAPI && len(usersList) > 0 {
		fmt.Println()
		fmt.Println("── Phase 3: API load test ──")
		runAPILoadTest(usersList, stats)
	}

	// Report
	printReport(stats)
}

func registerUsers(n int) []*User {
	usersList := make([]*User, 0, n)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)

	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()

			u := &User{
				Username: fmt.Sprintf("st_%d_%d", time.Now().UnixNano()%100000, idx),
				Password: "test123456",
				Email:    fmt.Sprintf("st_%d_%d@test.local", time.Now().UnixNano()%100000, idx),
				client:   &http.Client{Timeout: 15 * time.Second},
			}

			body, _ := json.Marshal(map[string]string{
				"username":         u.Username,
				"password":         u.Password,
				"email":            u.Email,
				"confirm_password": u.Password,
			})

			// Register (ignore error, user may already exist)
			resp, _ := u.client.Post(*baseURL+"/api/auth/register", "application/json", bytes.NewReader(body))
			if resp != nil {
				resp.Body.Close()
			}

			// Login
			resp, err := u.client.Post(*baseURL+"/api/auth/login", "application/json", bytes.NewReader(body))
			if err != nil || resp == nil {
				return
			}
			defer resp.Body.Close()

			var lr LoginResp
			if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
				return
			}
			u.Token = lr.Token

			mu.Lock()
			usersList = append(usersList, u)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	return usersList
}

func runSubmissionStress(usersList []*User, stats *Stats) {
	// Code templates with proper newlines
	codes := map[string]string{
		"AC": `#include <iostream>
int main() { int a, b; std::cin >> a >> b; std::cout << a + b << std::endl; return 0; }`,
		"WA": `#include <iostream>
int main() { int a, b; std::cin >> a >> b; std::cout << a * b << std::endl; return 0; }`,
		"TLE": `#include <iostream>
int main() { while(true) {} return 0; }`,
		"RE": `#include <iostream>
int main() { int* p = nullptr; *p = 42; return 0; }`,
		"python_ac": `import sys
a, b = map(int, sys.stdin.read().split())
print(a + b)`,
		"python_tle": `while True:
    pass`,
	}

	languages := []string{"cpp", "python"}
	codeTypes := []string{"AC", "WA", "TLE", "RE"}

	var wg sync.WaitGroup
	taskCh := make(chan struct {
		user     *User
		codeType string
		language string
	}, *concur*2)

	// Progress ticker
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				s := stats.TotalSubmitted.Load()
				c := stats.TotalCompleted.Load()
				a := stats.Accepted.Load()
				e := stats.Errors.Load()
				fmt.Printf("\r  Sent: %d | Done: %d | AC: %d | Errors: %d   ", s, c, a, e)
			}
		}
	}()

	// Consumers
	for i := 0; i < *concur; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				code := codes[t.codeType]
				if t.language == "python" {
					if t.codeType == "AC" {
						code = codes["python_ac"]
					} else if t.codeType == "TLE" {
						code = codes["python_tle"]
					}
				}
				submitOne(t.user, t.language, code, stats)
			}
		}()
	}

	// Producer
	go func() {
		total := int64(0)
		if *duration > 0 {
			deadline := time.Now().Add(*duration)
			for time.Now().Before(deadline) {
				u := usersList[rand.Intn(len(usersList))]
				ct := codeTypes[rand.Intn(len(codeTypes))]
				lang := languages[rand.Intn(len(languages))]
				taskCh <- struct {
					user     *User
					codeType string
					language string
				}{u, ct, lang}
				total++
				stats.TotalSubmitted.Store(total)
			}
		} else {
			for i := 0; i < *subs; i++ {
				u := usersList[rand.Intn(len(usersList))]
				ct := codeTypes[rand.Intn(len(codeTypes))]
				lang := languages[rand.Intn(len(languages))]
				taskCh <- struct {
					user     *User
					codeType string
					language string
				}{u, ct, lang}
				total++
				stats.TotalSubmitted.Store(total)
			}
		}
		close(taskCh)
	}()

	// Handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\n  Interrupted — waiting for in-flight submissions...")
	}()

	wg.Wait()
	close(done)

	fmt.Printf("\r  Sent: %d | Done: %d | AC: %d | Errors: %d   \n",
		stats.TotalSubmitted.Load(), stats.TotalCompleted.Load(),
		stats.Accepted.Load(), stats.Errors.Load())
}

func submitOne(user *User, language, code string, stats *Stats) {
	reqBody, _ := json.Marshal(SubmissionReq{
		ProblemID: *problemID,
		Language:  language,
		Code:      code,
	})

	start := time.Now()
	req, _ := http.NewRequest("POST", *baseURL+"/api/submissions", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+user.Token)

	resp, err := user.client.Do(req)
	if err != nil {
		stats.Errors.Add(1)
		return
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		stats.Errors.Add(1)
		return
	}

	var sr struct {
		ID     int    `json:"submission_id"`
		Status string `json:"status"`
	}
	json.Unmarshal(data, &sr)

	if sr.ID == 0 {
		stats.Errors.Add(1)
		return
	}

	latency := time.Since(start)
	stats.TotalLatencyMs.Add(latency.Milliseconds())
	recordLatency(stats, latency)

	pollSubmission(user, sr.ID, stats)
}

func pollSubmission(user *User, id int, stats *Stats) {
	for i := 0; i < 60; i++ {
		time.Sleep(500 * time.Millisecond)

		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/submissions/%d", *baseURL, id), nil)
		req.Header.Set("Authorization", "Bearer "+user.Token)

		resp, err := user.client.Do(req)
		if err != nil {
			continue
		}

		var sub struct {
			Status string `json:"status"`
		}
		json.NewDecoder(resp.Body).Decode(&sub)
		resp.Body.Close()

		switch sub.Status {
		case "Accepted":
			stats.Accepted.Add(1)
			stats.TotalCompleted.Add(1)
			return
		case "Wrong Answer":
			stats.WrongAnswer.Add(1)
			stats.TotalCompleted.Add(1)
			return
		case "Time Limit Exceeded":
			stats.TLE.Add(1)
			stats.TotalCompleted.Add(1)
			return
		case "Memory Limit Exceeded":
			stats.MLE.Add(1)
			stats.TotalCompleted.Add(1)
			return
		case "Runtime Error":
			stats.RuntimeError.Add(1)
			stats.TotalCompleted.Add(1)
			return
		case "Compile Error":
			stats.CompileError.Add(1)
			stats.TotalCompleted.Add(1)
			return
		case "pending", "judging":
			continue
		default:
			continue
		}
	}
	stats.Pending.Add(1)
}

func runAPILoadTest(usersList []*User, stats *Stats) {
	authUsers := usersList
	if len(authUsers) > 5 {
		authUsers = authUsers[:5]
	}

	tests := []struct {
		name   string
		method string
		path   string
		auth   bool
	}{
		{"Problems List", "GET", "/api/problems", false},
		{"Problem Detail", "GET", fmt.Sprintf("/api/problems/%d", *problemID), false},
		{"Leaderboard", "GET", "/api/leaderboard", false},
		{"Contests List", "GET", "/api/contests", false},
		{"Submissions List", "GET", "/api/submissions", true},
		{"Profile", "GET", "/api/profile", true},
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, *apiConcur)
	dur := 15 * time.Second

	fmt.Printf("  Running for %v with %d workers...\n", dur, *apiConcur)

	for _, t := range tests {
		wg.Add(1)
		go func(test struct {
			name   string
			method string
			path   string
			auth   bool
		}) {
			defer wg.Done()
			client := &http.Client{Timeout: 10 * time.Second}
			deadline := time.After(dur)

			for {
				select {
				case <-deadline:
					return
				default:
				}
				sem <- struct{}{}

				req, _ := http.NewRequest(test.method, *baseURL+test.path, nil)
				if test.auth {
					u := authUsers[rand.Intn(len(authUsers))]
					req.Header.Set("Authorization", "Bearer "+u.Token)
				}

				start := time.Now()
				resp, err := client.Do(req)
				<-sem

				if err != nil {
					stats.APIFail.Add(1)
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				stats.APITotalLatency.Add(time.Since(start).Microseconds())
				if resp.StatusCode < 400 {
					stats.APISuccess.Add(1)
				} else {
					stats.APIFail.Add(1)
				}
			}
		}(t)
	}
	wg.Wait()
}

func recordLatency(stats *Stats, d time.Duration) {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	switch {
	case d < 100*time.Millisecond:
		stats.latencyBuckets["<100ms"].Add(1)
	case d < 500*time.Millisecond:
		stats.latencyBuckets["<500ms"].Add(1)
	case d < 1*time.Second:
		stats.latencyBuckets["<1s"].Add(1)
	case d < 3*time.Second:
		stats.latencyBuckets["<3s"].Add(1)
	case d < 10*time.Second:
		stats.latencyBuckets["<10s"].Add(1)
	default:
		stats.latencyBuckets[">=10s"].Add(1)
	}
}

func printReport(stats *Stats) {
	fmt.Println()
	fmt.Println("═══════════════════════════════════════")
	fmt.Println("           STRESS TEST REPORT          ")
	fmt.Println("═══════════════════════════════════════")

	if !*skipSub {
		fmt.Println()
		fmt.Println("── Submissions ──")
		total := stats.TotalSubmitted.Load()
		done := stats.TotalCompleted.Load()
		fmt.Printf("  Total sent:       %d\n", total)
		fmt.Printf("  Completed:        %d (%.1f%%)\n", done, percent(done, total))
		fmt.Printf("  Still pending:    %d\n", stats.Pending.Load())
		fmt.Printf("  HTTP Errors:      %d\n\n", stats.Errors.Load())

		fmt.Println("  Verdicts:")
		ac := stats.Accepted.Load()
		wa := stats.WrongAnswer.Load()
		tle := stats.TLE.Load()
		mle := stats.MLE.Load()
		re := stats.RuntimeError.Load()
		ce := stats.CompileError.Load()
		fmt.Printf("    Accepted:        %d (%.1f%%)\n", ac, percent(ac, done))
		fmt.Printf("    Wrong Answer:    %d (%.1f%%)\n", wa, percent(wa, done))
		fmt.Printf("    TLE:             %d (%.1f%%)\n", tle, percent(tle, done))
		fmt.Printf("    MLE:             %d (%.1f%%)\n", mle, percent(mle, done))
		fmt.Printf("    Runtime Error:   %d (%.1f%%)\n", re, percent(re, done))
		fmt.Printf("    Compile Error:   %d (%.1f%%)\n", ce, percent(ce, done))

		if done > 0 {
			fmt.Println()
			fmt.Println("  Latency (submit → verdict):")
			avg := stats.TotalLatencyMs.Load() / done
			fmt.Printf("    Average:         %dms\n", avg)
			stats.mu.Lock()
			keys := make([]string, 0, len(stats.latencyBuckets))
			for k := range stats.latencyBuckets {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool {
				order := map[string]int{"<100ms": 0, "<500ms": 1, "<1s": 2, "<3s": 3, "<10s": 4, ">=10s": 5}
				return order[keys[i]] < order[keys[j]]
			})
			for _, k := range keys {
				n := stats.latencyBuckets[k].Load()
				fmt.Printf("    %-10s       %d (%.1f%%)\n", k, n, percent(n, done))
			}
			stats.mu.Unlock()
		}
	}

	if !*skipAPI {
		fmt.Println()
		fmt.Println("── API Load Test (15s) ──")
		success := stats.APISuccess.Load()
		fail := stats.APIFail.Load()
		totalAPI := success + fail
		fmt.Printf("  Total requests:   %d\n", totalAPI)
		fmt.Printf("  Success:          %d (%.1f%%)\n", success, percent(success, totalAPI))
		fmt.Printf("  Failed:           %d\n", fail)
		if totalAPI > 0 {
			avgLat := float64(stats.APITotalLatency.Load()) / float64(totalAPI) / 1000
			fmt.Printf("  Avg latency:      %.1fms\n", avgLat)
			fmt.Printf("  Throughput:       %.1f req/s\n", float64(totalAPI)/15.0)
		}
	}

	fmt.Println()
	fmt.Println("═══════════════════════════════════════")
}

func percent(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

var _ = strings.Join
