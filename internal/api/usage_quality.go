package api

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// QualityValues are the mean quality values of a set of ended tasks. Each round, state and token value is the mean
// for each task. A value that no task has is null.
type QualityValues struct {
	// Tasks is the number of ended tasks
	Tasks int64 `gork:"tasks"`
	// Merged is the number of tasks whose pull request merged
	Merged int64 `gork:"merged"`
	// ReviewRounds is the mean number of Reviewer sessions that ended with done
	ReviewRounds float64 `gork:"reviewRounds"`
	// FixRounds is the mean number of Implementer sessions after the first one. A task with one Implementer session
	// has 0
	FixRounds float64 `gork:"fixRounds"`
	// NeedsHuman is the mean number of needs_human states
	NeedsHuman float64 `gork:"needsHuman"`
	// Questions is the mean number of asked states
	Questions float64 `gork:"questions"`
	// WorkSeconds is the mean time from the first working state to the end, for the tasks that have a working state
	WorkSeconds *float64 `gork:"workSeconds"`
	// InputTokens is the mean sum of the input tokens, for the tasks that have a value
	InputTokens *float64 `gork:"inputTokens"`
	// OutputTokens is the mean sum of the output tokens, for the tasks that have a value
	OutputTokens *float64 `gork:"outputTokens"`
	// CacheReadTokens is the mean sum of the cache read tokens, for the tasks that have a value
	CacheReadTokens *float64 `gork:"cacheReadTokens"`
	// CacheWriteTokens is the mean sum of the cache write tokens, for the tasks that have a value
	CacheWriteTokens *float64 `gork:"cacheWriteTokens"`
	// CostUSD is the mean cost in USD, for the tasks that have a value
	CostUSD *float64 `gork:"costUsd"`
}

// QualityDay is the quality of one group on the day of the end of its tasks.
type QualityDay struct {
	// Day is the day of the end of the tasks, as "YYYY-MM-DD" in the time zone of the request
	Day string `gork:"day"`
	// Group is the value of each selected field, in the order of the fields of the route. With no field it is empty
	Group  []string      `gork:"group"`
	Values QualityValues `gork:"values"`
}

// QualityGroup is the quality of one group in the period.
type QualityGroup struct {
	// Group is the value of each selected field, in the order of the fields of the route. With no field it is empty
	Group  []string      `gork:"group"`
	Values QualityValues `gork:"values"`
}

// QualityOptions are the values that each filter can take in the period.
type QualityOptions struct {
	// Harnesses are the Harnesses of the tasks
	Harnesses []string `gork:"harnesses"`
	// Models are the models of the tasks
	Models []string `gork:"models"`
	// Efforts are the efforts of the tasks. A request cannot filter for the tasks with no effort
	Efforts []string `gork:"efforts"`
	// Repositories are the repositories of the tasks as "owner/name"
	Repositories []string `gork:"repositories"`
}

// Quality is the quality statistics of the ended tasks in a period.
type Quality struct {
	// Totals are the values of all tasks that pass the filters
	Totals QualityValues `gork:"totals"`
	// Days are the values of each day and each group, the oldest day first
	Days []QualityDay `gork:"days"`
	// Groups are the values of each group
	Groups []QualityGroup `gork:"groups"`
	// Options are the values that each filter can take, before the filters
	Options QualityOptions `gork:"options"`
}

// QualityQuery is the query of GetUsageQuality.
type QualityQuery struct {
	// From is the start of the period. A task is in the period when its end is From or later
	From time.Time `gork:"from" validate:"required"`
	// To is the end of the period. A task is in the period when its end is before To
	To time.Time `gork:"to" validate:"required"`
	// Tz is the IANA name of the time zone for the days. The default is UTC
	Tz string `gork:"tz"`
	// Harness limits the tasks to these Harnesses
	Harness []string `gork:"harness"`
	// Model limits the tasks to these models
	Model []string `gork:"model"`
	// Effort limits the tasks to these efforts
	Effort []string `gork:"effort"`
	// Repository limits the tasks to these repositories as "owner/name"
	Repository []string `gork:"repository"`
	// Group is the list of fields that splits the days and the groups. With no Group, all tasks are in one group
	Group []string `gork:"group" validate:"omitempty,dive,oneof=harness model effort repository"`
}

// GetUsageQualityRequest is the request of GetUsageQuality.
type GetUsageQualityRequest struct {
	Query QualityQuery
}

// GetUsageQualityResponse is the response of GetUsageQuality.
type GetUsageQualityResponse struct {
	Body Envelope[Quality]
}

var qualityFields = []string{"harness", "model", "effort", "repository"}

// GetUsageQuality returns the quality values of the ended tasks that ended in the period.
func (h *handlers) GetUsageQuality(ctx context.Context, req GetUsageQualityRequest) (*GetUsageQualityResponse, error) {
	query := req.Query
	location, err := usageLocation(query.Tz)
	if err != nil {
		return nil, err
	}
	rows, err := h.queries.ListEndedTaskQuality(ctx)
	if err != nil {
		return nil, err
	}

	var (
		options     QualityOptions
		totals      qualityTasks
		days        = map[usageDayKey]qualityTasks{}
		groups      = map[groupKey]qualityTasks{}
		groupValues = map[groupKey][]string{}
	)
	for _, row := range rows {
		ended, err := time.Parse(time.RFC3339Nano, row.EndedAt)
		if err != nil {
			return nil, err
		}
		if ended.Before(query.From) || !ended.Before(query.To) {
			continue
		}
		harness, model, effort := row.Harness.String, row.Model.String, row.Effort.String
		options.Harnesses = appendNonEmpty(options.Harnesses, harness)
		options.Models = appendNonEmpty(options.Models, model)
		options.Efforts = appendNonEmpty(options.Efforts, effort)
		options.Repositories = appendNonEmpty(options.Repositories, row.Repository)
		if !passes(query.Harness, harness) || !passes(query.Model, model) ||
			!passes(query.Effort, effort) || !passes(query.Repository, row.Repository) {
			continue
		}
		task := qualityTask{
			merged:           row.Merged,
			reviewRounds:     float64(row.ReviewRounds),
			fixRounds:        float64(max(row.ImplementerSessions-1, 0)),
			needsHuman:       float64(row.NeedsHuman),
			questions:        float64(row.Questions),
			inputTokens:      nullableFloat(row.InputTokens),
			outputTokens:     nullableFloat(row.OutputTokens),
			cacheReadTokens:  nullableFloat(row.CacheReadTokens),
			cacheWriteTokens: nullableFloat(row.CacheWriteTokens),
			costUSD:          nullableFloat(row.CostUsd),
		}
		if row.WorkingStartedAt.Valid {
			started, err := time.Parse(time.RFC3339Nano, row.WorkingStartedAt.String)
			if err != nil {
				return nil, err
			}
			seconds := ended.Sub(started).Seconds()
			task.workSeconds = &seconds
		}
		group := qualityGroupValues(row, query.Group)
		encoded := keyOfGroup(group)
		key := usageDayKey{day: ended.In(location).Format(time.DateOnly), group: encoded}
		groupValues[encoded] = group
		totals = append(totals, task)
		days[key] = append(days[key], task)
		groups[encoded] = append(groups[encoded], task)
	}

	quality := Quality{Totals: totals.values(), Days: []QualityDay{}, Groups: []QualityGroup{}, Options: QualityOptions{
		Harnesses:    distinct(options.Harnesses),
		Models:       distinct(options.Models),
		Efforts:      distinct(options.Efforts),
		Repositories: distinct(options.Repositories),
	}}
	for key, tasks := range days {
		quality.Days = append(quality.Days, QualityDay{Day: key.day, Group: groupValues[key.group], Values: tasks.values()})
	}
	slices.SortFunc(quality.Days, func(a, b QualityDay) int {
		return cmp.Or(cmp.Compare(a.Day, b.Day), slices.Compare(a.Group, b.Group))
	})
	for group, tasks := range groups {
		quality.Groups = append(quality.Groups, QualityGroup{Group: groupValues[group], Values: tasks.values()})
	}
	slices.SortFunc(quality.Groups, func(a, b QualityGroup) int { return slices.Compare(a.Group, b.Group) })
	return &GetUsageQualityResponse{Body: Envelope[Quality]{Data: quality}}, nil
}

func qualityGroupValues(row store.ListEndedTaskQualityRow, fields []string) []string {
	values := make([]string, 0, len(fields))
	for _, field := range selectedFields(fields, qualityFields) {
		switch field {
		case "harness":
			values = append(values, row.Harness.String)
		case "model":
			values = append(values, row.Model.String)
		case "effort":
			values = append(values, row.Effort.String)
		case "repository":
			values = append(values, row.Repository)
		}
	}
	return values
}

// qualityTask is the quality of one ended task.
type qualityTask struct {
	merged           bool
	reviewRounds     float64
	fixRounds        float64
	needsHuman       float64
	questions        float64
	workSeconds      *float64
	inputTokens      *float64
	outputTokens     *float64
	cacheReadTokens  *float64
	cacheWriteTokens *float64
	costUSD          *float64
}

type qualityTasks []qualityTask

func (tasks qualityTasks) values() QualityValues {
	var (
		values                                         QualityValues
		reviewRounds, fixRounds, needsHuman, questions float64
		work, input, output, cacheRead, cacheWrite     qualityMean
		cost                                           qualityMean
	)
	for _, task := range tasks {
		values.Tasks++
		if task.merged {
			values.Merged++
		}
		reviewRounds += task.reviewRounds
		fixRounds += task.fixRounds
		needsHuman += task.needsHuman
		questions += task.questions
		work.add(task.workSeconds)
		input.add(task.inputTokens)
		output.add(task.outputTokens)
		cacheRead.add(task.cacheReadTokens)
		cacheWrite.add(task.cacheWriteTokens)
		cost.add(task.costUSD)
	}
	values.ReviewRounds = meanOf(reviewRounds, values.Tasks)
	values.FixRounds = meanOf(fixRounds, values.Tasks)
	values.NeedsHuman = meanOf(needsHuman, values.Tasks)
	values.Questions = meanOf(questions, values.Tasks)
	values.WorkSeconds = work.value()
	values.InputTokens = input.value()
	values.OutputTokens = output.value()
	values.CacheReadTokens = cacheRead.value()
	values.CacheWriteTokens = cacheWrite.value()
	values.CostUSD = cost.value()
	return values
}

// qualityMean is the mean of the tasks that have a value.
type qualityMean struct {
	sum   float64
	count int64
}

func (m *qualityMean) add(value *float64) {
	if value != nil {
		m.sum += *value
		m.count++
	}
}

func (m qualityMean) value() *float64 {
	if m.count == 0 {
		return nil
	}
	mean := m.sum / float64(m.count)
	return &mean
}

func meanOf(sum float64, count int64) float64 {
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}
