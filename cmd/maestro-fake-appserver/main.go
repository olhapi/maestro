package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
)

func main() {
	var scenario string
	var delayMS int
	flag.StringVar(&scenario, "scenario", "complete", "Scenario to run: complete, input, or stall")
	flag.IntVar(&delayMS, "delay-ms", 0, "Optional delay before emitting turn events")
	flag.Parse()

	runScenario, err := scenarioForMode(scenario, delayMS)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if err := fakeappserver.RunScenario(os.Stdin, os.Stdout, os.Stderr, os.Getenv("TRACE_FILE"), nil, runScenario); err != nil {
		var exitErr *fakeappserver.ExitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func scenarioForMode(mode string, delayMS int) (fakeappserver.Scenario, error) {
	threadID := "thread-" + sanitizeScenario(mode)
	turnID := "turn-" + sanitizeScenario(mode)

	scenario := fakeappserver.Scenario{
		Steps: []fakeappserver.Step{
			{
				Match: fakeappserver.Match{Method: "initialize"},
				Emit:  []fakeappserver.Output{{JSON: map[string]interface{}{"id": 1, "result": map[string]interface{}{"ok": true}}}},
			},
			{Match: fakeappserver.Match{Method: "initialized"}},
			{
				Match: fakeappserver.Match{Method: "thread/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{
						"id": 2,
						"result": map[string]interface{}{
							"thread": map[string]interface{}{"id": threadID},
						},
					},
				}},
			},
			{
				Match: fakeappserver.Match{Method: "turn/start"},
				Emit: []fakeappserver.Output{{
					JSON: map[string]interface{}{
						"id": 3,
						"result": map[string]interface{}{
							"turn": map[string]interface{}{"id": turnID},
						},
					},
				}},
				DelayMS: delayMS,
			},
		},
	}

	switch sanitizeScenario(mode) {
	case "complete":
		scenario.ExitAfterLastStep = true
		scenario.Steps[3].EmitAfterDelay = []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"method": "turn/completed",
				"params": map[string]interface{}{
					"threadId": threadID,
					"turn": map[string]interface{}{
						"id":     turnID,
						"status": "completed",
						"items":  []interface{}{},
					},
				},
			},
		}}
	case "input":
		scenario.Steps[3].EmitAfterDelay = []fakeappserver.Output{{
			JSON: map[string]interface{}{
				"id":     1000,
				"method": "item/tool/requestUserInput",
				"params": map[string]interface{}{
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
			},
		}}
	case "stall":
		// No additional output. The helper keeps reading stdin and stays alive.
	default:
		return fakeappserver.Scenario{}, fmt.Errorf("unknown scenario %q", mode)
	}

	return scenario, nil
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
