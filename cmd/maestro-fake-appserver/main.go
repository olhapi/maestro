package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

type envelope struct {
	ID     interface{}            `json:"id,omitempty"`
	Method string                 `json:"method,omitempty"`
	Params map[string]interface{} `json:"params,omitempty"`
	Result map[string]interface{} `json:"result,omitempty"`
}

func main() {
	var scenario string
	var delayMs int
	flag.StringVar(&scenario, "scenario", "complete", "Scenario to run: complete, input, or stall")
	flag.IntVar(&delayMs, "delay-ms", 0, "Optional delay before emitting turn events")
	flag.Parse()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	threadID := "thread-" + sanitizeScenario(scenario)
	turnID := "turn-" + sanitizeScenario(scenario)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var req envelope
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}

		switch req.Method {
		case "initialize":
			emit(envelope{ID: req.ID, Result: map[string]interface{}{"ok": true}})
		case "initialized":
		case "thread/start":
			emit(envelope{
				ID: req.ID,
				Result: map[string]interface{}{
					"thread": map[string]interface{}{"id": threadID},
				},
			})
		case "turn/start":
			emit(envelope{
				ID: req.ID,
				Result: map[string]interface{}{
					"turn": map[string]interface{}{"id": turnID},
				},
			})
			if delayMs > 0 {
				time.Sleep(time.Duration(delayMs) * time.Millisecond)
			}
			switch sanitizeScenario(scenario) {
			case "complete":
				emit(envelope{
					Method: "turn/completed",
					Params: map[string]interface{}{
						"threadId": threadID,
						"turn": map[string]interface{}{
							"id":     turnID,
							"status": "completed",
							"items":  []interface{}{},
						},
					},
				})
			case "input":
				emit(envelope{
					ID:     1000,
					Method: "item/tool/requestUserInput",
					Params: map[string]interface{}{
						"questions": []map[string]interface{}{{
							"id":       "strategy",
							"header":   "Strategy",
							"question": "Which strategy should I use?",
							"options": []map[string]interface{}{
								{"label": "Option A", "description": "Use approach A"},
								{"label": "Option B", "description": "Use approach B"},
							},
						}},
					},
				})
				sleepForever()
			case "stall":
				sleepForever()
			default:
				fmt.Fprintf(os.Stderr, "unknown scenario %q\n", scenario)
				os.Exit(2)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func emit(payload envelope) {
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println(string(body))
}

func sanitizeScenario(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "complete":
		return "complete"
	case "input", "input-required", "input_required":
		return "input"
	case "stall":
		return "stall"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func sleepForever() {
	for {
		time.Sleep(time.Hour)
	}
}
