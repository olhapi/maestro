package runtimeview

import (
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

func buildActivityGroups(entries []kanban.IssueActivityEntry, runtimeEvents []kanban.RuntimeEvent) ([]kanban.ActivityGroup, []kanban.ActivityGroup) {
	mainByAttempt := map[int][]kanban.ActivityEntry{}
	debugByAttempt := map[int][]kanban.ActivityEntry{}
	attemptOrder := []int{}
	seenAttempt := map[int]bool{}

	for _, entry := range entries {
		if !seenAttempt[entry.Attempt] {
			seenAttempt[entry.Attempt] = true
			attemptOrder = append(attemptOrder, entry.Attempt)
		}
		projected := kanban.ActivityEntry{
			ID:         entry.ID,
			Kind:       entry.Kind,
			ItemType:   entry.ItemType,
			Phase:      entry.Phase,
			Status:     entry.Status,
			Title:      entry.Title,
			Summary:    entry.Summary,
			Detail:     entry.Detail,
			Expandable: entry.Expandable,
			Tone:       entry.Tone,
		}
		if entry.StartedAt != nil {
			projected.StartedAt = entry.StartedAt.UTC().Format(time.RFC3339)
		}
		if entry.CompletedAt != nil {
			projected.CompletedAt = entry.CompletedAt.UTC().Format(time.RFC3339)
		}
		if entry.Tier == "secondary" {
			debugByAttempt[entry.Attempt] = append(debugByAttempt[entry.Attempt], projected)
			continue
		}
		mainByAttempt[entry.Attempt] = append(mainByAttempt[entry.Attempt], projected)
	}

	meta := buildAttemptMetadata(runtimeEvents)
	mainGroups := make([]kanban.ActivityGroup, 0, len(attemptOrder))
	debugGroups := make([]kanban.ActivityGroup, 0, len(attemptOrder))
	for _, attempt := range attemptOrder {
		if entries := mainByAttempt[attempt]; len(entries) > 0 {
			group := kanban.ActivityGroup{Attempt: attempt, Entries: entries}
			if attemptMeta, ok := meta[attempt]; ok {
				group.Phase = attemptMeta.phase
				group.Status = attemptMeta.status
			}
			mainGroups = append(mainGroups, group)
		}
		if entries := debugByAttempt[attempt]; len(entries) > 0 {
			group := kanban.ActivityGroup{Attempt: attempt, Entries: entries}
			if attemptMeta, ok := meta[attempt]; ok {
				group.Phase = attemptMeta.phase
				group.Status = attemptMeta.status
			}
			debugGroups = append(debugGroups, group)
		}
	}
	return mainGroups, debugGroups
}

type attemptMetadata struct {
	phase  string
	status string
}

func buildAttemptMetadata(events []kanban.RuntimeEvent) map[int]attemptMetadata {
	out := map[int]attemptMetadata{}
	for _, event := range events {
		if event.Attempt == 0 {
			continue
		}
		meta := out[event.Attempt]
		if meta.phase == "" && event.Phase != "" {
			meta.phase = event.Phase
		}
		switch event.Kind {
		case "run_completed":
			meta.status = "completed"
		case "run_failed", "run_unsuccessful", "run_interrupted":
			meta.status = "failed"
		case "retry_paused":
			meta.status = "paused"
		case "retry_scheduled":
			if meta.status == "" {
				meta.status = "scheduled"
			}
		case "run_started":
			if meta.status == "" {
				meta.status = "active"
			}
		}
		out[event.Attempt] = meta
	}
	return out
}
