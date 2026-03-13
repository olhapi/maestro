package kanban

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var recurringCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func normalizeCronSpec(spec string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(spec)), " ")
}

func ValidateRecurringCron(spec string) error {
	spec = normalizeCronSpec(spec)
	if spec == "" {
		return validationErrorf("cron is required for recurring issues")
	}
	if _, err := recurringCronParser.Parse(spec); err != nil {
		return validationErrorf("invalid cron %q: %v", spec, err)
	}
	return nil
}

func NextRecurringRun(spec string, from time.Time, loc *time.Location) (time.Time, error) {
	spec = normalizeCronSpec(spec)
	if err := ValidateRecurringCron(spec); err != nil {
		return time.Time{}, err
	}
	if loc == nil {
		loc = time.Local
	}
	schedule, err := recurringCronParser.Parse(spec)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse recurring cron: %w", err)
	}
	next := schedule.Next(from.In(loc))
	if next.IsZero() {
		return time.Time{}, validationErrorf("cron %q produced no future occurrence", spec)
	}
	return next.UTC(), nil
}
