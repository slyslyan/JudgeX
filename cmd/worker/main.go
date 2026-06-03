package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"judgex/internal/config"
	"judgex/internal/database"
	"judgex/internal/judge"
	"judgex/internal/model"
	"judgex/internal/queue"
	"judgex/internal/sandbox"
)

func main() {
	if sandbox.SandboxInit() {
		return
	}

	cfg := config.Load()
	database.Init(cfg)

	queue.Init(func(task queue.JudgeTask) {
		log.Printf("[worker] judging submission #%d (lang=%s)", task.SubmissionID, task.Language)
		processJudgeTask(task)
	})

	log.Println("[worker] Judge worker started, waiting for tasks...")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("[worker] Shutting down...")
	queue.Stop()
}

func processJudgeTask(task queue.JudgeTask) {
	var testCases []model.TestCase
	database.DB.Where("problem_id = ?", task.ProblemID).Find(&testCases)
	if len(testCases) == 0 {
		database.DB.Model(&model.Submission{}).Where("id = ?", task.SubmissionID).
			Updates(map[string]interface{}{"status": "No Test Cases"})
		return
	}

	maxTime := 0
	maxMem := 0

	for _, tc := range testCases {
		result := judge.Run(task.Language, task.Code, tc.Input, task.TimeLimit, task.MemoryLimit)

		if result.Status != judge.StatusAccepted {
			database.DB.Model(&model.Submission{}).Where("id = ?", task.SubmissionID).
				Updates(map[string]interface{}{
					"status":      result.Status,
					"time_used":   result.TimeUsed,
					"memory_used": result.MemoryUsed,
				})
			log.Printf("[worker] submission #%d: %s (test case #%d)", task.SubmissionID, result.Status, tc.ID)
			return
		}

		if err := judge.CompareOutput(tc.Expected, result.Output); err != nil {
			database.DB.Model(&model.Submission{}).Where("id = ?", task.SubmissionID).
				Updates(map[string]interface{}{
					"status":      judge.StatusWrongAnswer,
					"time_used":   result.TimeUsed,
					"memory_used": result.MemoryUsed,
				})
			log.Printf("[worker] submission #%d: Wrong Answer (test case #%d)", task.SubmissionID, tc.ID)
			return
		}

		if result.TimeUsed > maxTime {
			maxTime = result.TimeUsed
		}
		if result.MemoryUsed > maxMem {
			maxMem = result.MemoryUsed
		}
	}

	database.DB.Model(&model.Submission{}).Where("id = ?", task.SubmissionID).
		Updates(map[string]interface{}{
			"status":      judge.StatusAccepted,
			"time_used":   maxTime,
			"memory_used": maxMem,
		})
	log.Printf("[worker] submission #%d: Accepted (time=%dms)", task.SubmissionID, maxTime)
}
