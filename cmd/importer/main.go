package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm/clause"

	"judgex/internal/config"
	"judgex/internal/database"
	"judgex/internal/model"
)

type CFProblem struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Description   string      `json:"description"`
	TimeLimit     float64     `json:"time_limit"`
	MemoryLimit   float64     `json:"memory_limit"`
	Examples      []CFExample `json:"examples"`
	Tags          []string    `json:"tags"`
	Rating        *int64      `json:"rating"`
	OfficialTests []CFExample `json:"official_tests"`
	Note          *string     `json:"note"`
	InputFormat   *string     `json:"input_format"`
	OutputFormat  *string     `json:"output_format"`
}

type CFExample struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: importer <json_file> [max_problems]")
	}

	jsonFile := os.Args[1]
	maxProblems := 200
	if len(os.Args) >= 3 {
		fmt.Sscanf(os.Args[2], "%d", &maxProblems)
	}

	cfg := config.Load()
	if _, err := os.Stat(jsonFile); os.IsNotExist(err) {
		log.Fatalf("JSON file not found: %s", jsonFile)
	}

	database.Init(cfg)
	db := database.DB

	// AutoMigrate to ensure tables exist
	if err := db.AutoMigrate(&model.Problem{}, &model.ProblemTag{}); err != nil {
		log.Fatalf("AutoMigrate failed: %v", err)
	}

	data, err := os.ReadFile(jsonFile)
	if err != nil {
		log.Fatalf("Failed to read JSON: %v", err)
	}

	var problems []CFProblem
	if err := json.Unmarshal(data, &problems); err != nil {
		log.Fatalf("Failed to parse JSON: %v", err)
	}

	if len(problems) > maxProblems {
		problems = problems[:maxProblems]
	}

	log.Printf("Importing %d problems...", len(problems))

	testDataPath := cfg.TestDataPath
	if testDataPath == "" {
		testDataPath = "/home/sly/Downloads/oj/data/testcases"
	}

	imported := 0
	for i, cf := range problems {
		// Build description with all sections
		desc := cf.Description
		if cf.InputFormat != nil && *cf.InputFormat != "" {
			desc += "\n\n### Input Format\n\n" + *cf.InputFormat
		}
		if cf.OutputFormat != nil && *cf.OutputFormat != "" {
			desc += "\n\n### Output Format\n\n" + *cf.OutputFormat
		}
		if cf.Note != nil && *cf.Note != "" {
			desc += "\n\n### Note\n\n" + *cf.Note
		}

		// Time limit: duckdb gives seconds, convert to ms
		timeLimitMs := int(cf.TimeLimit * 1000)
		if timeLimitMs == 0 {
			timeLimitMs = 1000 // default 1s
		}

		// Memory limit: duckdb gives MB, keep as is
		memoryLimitMB := int(cf.MemoryLimit)
		if memoryLimitMB == 0 {
			memoryLimitMB = 128 // default 128MB
		}

		// Prepare sample cases (examples field)
		if len(cf.Examples) == 0 {
			// Skip problems without sample cases
			log.Printf("  [%d/%d] SKIP %s (no examples)", i+1, len(problems), cf.Title)
			continue
		}

		sampleCasesJSON, err := json.Marshal(cf.Examples)
		if err != nil {
			log.Printf("  [%d/%d] WARN %s: failed to marshal examples: %v", i+1, len(problems), cf.Title, err)
			continue
		}

		// Create or update problem
		p := model.Problem{
			Title:       cf.Title,
			Description: desc,
			TimeLimit:   timeLimitMs,
			MemoryLimit: memoryLimitMB,
			SampleCases: json.RawMessage(sampleCasesJSON),
		}

		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "title"}},
			DoNothing: true,
		}).Create(&p).Error; err != nil {
			log.Printf("  [%d/%d] FAIL %s: %v", i+1, len(problems), cf.Title, err)
			continue
		}

		// If the problem was already imported (OnConflict), fetch it
		if p.ID == 0 {
			db.Where("title = ?", cf.Title).First(&p)
		}

		// Save tags
		for _, tagName := range cf.Tags {
			tagName = strings.TrimSpace(tagName)
			if tagName == "" {
				continue
			}
			var tag model.ProblemTag
			db.Where("name = ?", tagName).FirstOrCreate(&tag, model.ProblemTag{Name: tagName})
			db.Exec("INSERT IGNORE INTO problem_tag_links (problem_id, tag_id) VALUES (?, ?)", p.ID, tag.ID)
		}

		// Save official test cases to disk
		if len(cf.OfficialTests) > 0 {
			problemDir := filepath.Join(testDataPath, fmt.Sprintf("%d", p.ID))
			if err := os.MkdirAll(problemDir, 0755); err != nil {
				log.Printf("  [%d/%d] WARN %s: failed to create dir %s: %v", i+1, len(problems), cf.Title, problemDir, err)
			} else {
				for tci, tc := range cf.OfficialTests {
					inFile := filepath.Join(problemDir, fmt.Sprintf("%d.in", tci+1))
					outFile := filepath.Join(problemDir, fmt.Sprintf("%d.out", tci+1))
					os.WriteFile(inFile, []byte(tc.Input), 0644)
					os.WriteFile(outFile, []byte(tc.Output), 0644)
				}
			}
		}

		imported++
		if imported%20 == 0 {
			log.Printf("  [%d/%d] Imported %d so far...", i+1, len(problems), imported)
		}
	}

	log.Printf("Done! Imported %d problems (skipped %d with no examples).", imported, len(problems)-imported)
	log.Printf("Test cases saved to: %s", testDataPath)
}
