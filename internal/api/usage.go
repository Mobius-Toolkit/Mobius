package api

import (
	"cmp"
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// utcSecond is the time layout of a whole second. A time of the layout time.RFC3339Nano has a variable number of
// fraction digits, so a text compare of two times in the same second can give the wrong order.
const utcSecond = "2006-01-02T15:04:05Z"

// UsageValues are the sums of the turn usage of a set of rows. A sum of tokens or of cost is null when no row has a
// value.
type UsageValues struct {
	// Sessions is the number of distinct sessions of the rows
	Sessions int64 `gork:"sessions"`
	// ContextMax is the largest context_used of the sessions of the rows, or null when no session has a value
	ContextMax *int64 `gork:"contextMax"`
	// ContextMaxSize is the context_size of the session that gave ContextMax
	ContextMaxSize *int64 `gork:"contextMaxSize"`
	// ContextMean is the mean context_used of the sessions with a value, rounded to a whole token
	ContextMean *int64 `gork:"contextMean"`
	// InputTokens is the sum of the input tokens
	InputTokens *int64 `gork:"inputTokens"`
	// OutputTokens is the sum of the output tokens
	OutputTokens *int64 `gork:"outputTokens"`
	// CacheReadTokens is the sum of the cache read tokens
	CacheReadTokens *int64 `gork:"cacheReadTokens"`
	// CacheWriteTokens is the sum of the cache write tokens
	CacheWriteTokens *int64 `gork:"cacheWriteTokens"`
	// CostUSD is the sum of the cost in USD
	CostUSD *float64 `gork:"costUsd"`
}

// UsageDay is the usage of one group on one day.
type UsageDay struct {
	// Day is the day of the start of the turns, as "YYYY-MM-DD" in the time zone of the request
	Day string `gork:"day"`
	// Group is the value of each selected field, in the order of the fields of the route. With no field it is empty
	Group  []string    `gork:"group"`
	Values UsageValues `gork:"values"`
}

// UsageGroup is the usage of one group in the period.
type UsageGroup struct {
	// Group is the value of each selected field, in the order of the fields of the route. With no field it is empty
	Group  []string    `gork:"group"`
	Values UsageValues `gork:"values"`
}

// UsageOptions are the values that each filter can take in the period.
type UsageOptions struct {
	// Harnesses are the Harnesses of the rows
	Harnesses []string `gork:"harnesses"`
	// Models are the models of the config
	Models []string `gork:"models"`
	// ModelVersions are the models that the Harness reports. A request cannot filter for the rows with no reported model
	ModelVersions []string `gork:"modelVersions"`
	// Efforts are the efforts of the rows. A request cannot filter for the rows with no effort
	Efforts []string `gork:"efforts"`
	// Roles are the roles of the rows
	Roles []string `gork:"roles"`
	// Repositories are the repositories of the rows as "owner/name"
	Repositories []string `gork:"repositories"`
}

// Usage is the token and cost statistics of the turns in a period.
type Usage struct {
	// Totals are the values of all rows that pass the filters
	Totals UsageValues `gork:"totals"`
	// Days are the values of each day and each group, the oldest day first
	Days []UsageDay `gork:"days"`
	// Groups are the values of each group
	Groups []UsageGroup `gork:"groups"`
	// Options are the values that each filter can take, before the filters
	Options UsageOptions `gork:"options"`
}

// UsageQuery is the query of GetUsage and of GetUsageTimes.
type UsageQuery struct {
	// From is the start of the period. A row is in the period when it started at From or later
	From time.Time `gork:"from" validate:"required"`
	// To is the end of the period. A row is in the period when it started before To
	To time.Time `gork:"to" validate:"required"`
	// Tz is the IANA name of the time zone for the days. The default is UTC
	Tz string `gork:"tz"`
	// Harness limits the rows to these Harnesses
	Harness []string `gork:"harness"`
	// Model limits the rows to these models of the config
	Model []string `gork:"model"`
	// ModelVersion limits the rows to these models that the Harness reports
	ModelVersion []string `gork:"modelVersion"`
	// Effort limits the rows to these efforts
	Effort []string `gork:"effort"`
	// Role limits the rows to these roles
	Role []string `gork:"role"`
	// Repository limits the rows to these repositories as "owner/name"
	Repository []string `gork:"repository"`
	// Group is the list of fields that splits the days and the groups. With no Group, all rows are in one group
	Group []string `gork:"group" validate:"omitempty,dive,oneof=harness model modelVersion effort role repository"`
}

// GetUsageRequest is the request of GetUsage.
type GetUsageRequest struct {
	Query UsageQuery
}

// GetUsageResponse is the response of GetUsage.
type GetUsageResponse struct {
	Body Envelope[Usage]
}

// GetUsage returns the tokens and the cost of the turns that started in the period.
func (h *handlers) GetUsage(ctx context.Context, req GetUsageRequest) (*GetUsageResponse, error) {
	query := req.Query
	location, err := usageLocation(query.Tz)
	if err != nil {
		return nil, err
	}
	rows, err := h.queries.ListTurnUsageBetween(ctx, store.ListTurnUsageBetweenParams{
		After:  query.From.Add(-time.Second).UTC().Format(utcSecond),
		Before: query.To.Add(time.Second).UTC().Format(utcSecond),
	})
	if err != nil {
		return nil, err
	}

	var (
		options     UsageOptions
		totals      usageSum
		days        = map[usageDayKey]*usageSum{}
		groups      = map[groupKey]*usageSum{}
		groupValues = map[groupKey][]string{}
	)
	for _, row := range rows {
		started, err := time.Parse(time.RFC3339Nano, row.StartedAt)
		if err != nil {
			return nil, err
		}
		if started.Before(query.From) || !started.Before(query.To) {
			continue
		}
		modelVersion := row.ReportedModel.String
		effort := row.Effort.String
		options.Harnesses = append(options.Harnesses, row.Harness)
		options.Models = append(options.Models, row.Model)
		if modelVersion != "" {
			options.ModelVersions = append(options.ModelVersions, modelVersion)
		}
		if effort != "" {
			options.Efforts = append(options.Efforts, effort)
		}
		options.Roles = append(options.Roles, row.Role)
		options.Repositories = append(options.Repositories, row.Repository)
		if !passes(query.Harness, row.Harness) || !passes(query.Model, row.Model) ||
			!passes(query.ModelVersion, modelVersion) || !passes(query.Effort, effort) || !passes(query.Role, row.Role) ||
			!passes(query.Repository, row.Repository) {
			continue
		}
		group := usageGroupValues(row, query.Group)
		encoded := keyOfGroup(group)
		key := usageDayKey{day: started.In(location).Format(time.DateOnly), group: encoded}
		if days[key] == nil {
			days[key] = &usageSum{}
		}
		if groups[encoded] == nil {
			groups[encoded] = &usageSum{}
		}
		groupValues[encoded] = group
		totals.add(row)
		days[key].add(row)
		groups[encoded].add(row)
	}

	usage := Usage{Totals: totals.values(), Days: []UsageDay{}, Groups: []UsageGroup{}, Options: UsageOptions{
		Harnesses:     distinct(options.Harnesses),
		Models:        distinct(options.Models),
		ModelVersions: distinct(options.ModelVersions),
		Efforts:       distinct(options.Efforts),
		Roles:         distinct(options.Roles),
		Repositories:  distinct(options.Repositories),
	}}
	for key, sum := range days {
		usage.Days = append(usage.Days, UsageDay{Day: key.day, Group: groupValues[key.group], Values: sum.values()})
	}
	slices.SortFunc(usage.Days, func(a, b UsageDay) int {
		return cmp.Or(cmp.Compare(a.Day, b.Day), slices.Compare(a.Group, b.Group))
	})
	for group, sum := range groups {
		usage.Groups = append(usage.Groups, UsageGroup{Group: groupValues[group], Values: sum.values()})
	}
	slices.SortFunc(usage.Groups, func(a, b UsageGroup) int { return slices.Compare(a.Group, b.Group) })
	return &GetUsageResponse{Body: Envelope[Usage]{Data: usage}}, nil
}

func usageLocation(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, api.NewHTTPError(http.StatusBadRequest, "The time zone "+name+" is not an IANA name.")
	}
	return location, nil
}

type usageDayKey struct {
	day   string
	group groupKey
}

// usageSum adds the usage rows of a set. A sum stays invalid until a row has a value.
type usageSum struct {
	sessions       map[int64]bool
	contextMax     sql.NullInt64
	contextMaxSize sql.NullInt64
	contextSum     int64
	contexts       int64
	input          sql.NullInt64
	output         sql.NullInt64
	cacheRead      sql.NullInt64
	cacheWrite     sql.NullInt64
	cost           sql.NullFloat64
}

func (s *usageSum) add(row store.ListTurnUsageBetweenRow) {
	if s.sessions == nil {
		s.sessions = map[int64]bool{}
	}
	first := !s.sessions[row.Session]
	s.sessions[row.Session] = true
	addInt(&s.input, row.InputTokens)
	addInt(&s.output, row.OutputTokens)
	addInt(&s.cacheRead, row.CacheReadTokens)
	addInt(&s.cacheWrite, row.CacheWriteTokens)
	if row.CostUsd.Valid {
		s.cost.Float64 += row.CostUsd.Float64
		s.cost.Valid = true
	}
	if first && row.SessionContextUsed.Valid {
		s.contexts++
		s.contextSum += row.SessionContextUsed.Int64
		if !s.contextMax.Valid || row.SessionContextUsed.Int64 > s.contextMax.Int64 {
			s.contextMax = row.SessionContextUsed
			s.contextMaxSize = row.SessionContextSize
		}
	}
}

func (s *usageSum) values() UsageValues {
	var mean sql.NullInt64
	if s.contexts > 0 {
		mean = sql.NullInt64{Int64: (s.contextSum + s.contexts/2) / s.contexts, Valid: true}
	}
	return UsageValues{
		Sessions:         int64(len(s.sessions)),
		ContextMax:       nullableInt(s.contextMax),
		ContextMaxSize:   nullableInt(s.contextMaxSize),
		ContextMean:      nullableInt(mean),
		InputTokens:      nullableInt(s.input),
		OutputTokens:     nullableInt(s.output),
		CacheReadTokens:  nullableInt(s.cacheRead),
		CacheWriteTokens: nullableInt(s.cacheWrite),
		CostUSD:          nullableFloat(s.cost),
	}
}

func addInt(sum *sql.NullInt64, value sql.NullInt64) {
	if value.Valid {
		sum.Int64 += value.Int64
		sum.Valid = true
	}
}

func nullableInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullableFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

// groupKey is an unambiguous key of the values of a group.
type groupKey string

func keyOfGroup(values []string) groupKey {
	var builder strings.Builder
	for _, value := range values {
		builder.WriteString(strconv.Itoa(len(value)))
		builder.WriteByte(':')
		builder.WriteString(value)
	}
	return groupKey(builder.String())
}

// selectedFields gives the fields of the request in the order of the route, with each field one time.
func selectedFields(fields []string, route []string) []string {
	selected := make([]string, 0, len(fields))
	for _, field := range route {
		if slices.Contains(fields, field) {
			selected = append(selected, field)
		}
	}
	return selected
}

var usageFields = []string{"harness", "model", "modelVersion", "effort", "role", "repository"}

func usageGroupValues(row store.ListTurnUsageBetweenRow, fields []string) []string {
	values := make([]string, 0, len(fields))
	for _, field := range selectedFields(fields, usageFields) {
		switch field {
		case "harness":
			values = append(values, row.Harness)
		case "model":
			values = append(values, row.Model)
		case "modelVersion":
			values = append(values, row.ReportedModel.String)
		case "effort":
			values = append(values, row.Effort.String)
		case "role":
			values = append(values, row.Role)
		case "repository":
			values = append(values, row.Repository)
		}
	}
	return values
}

func timeGroupValues(row timeRow, fields []string) []string {
	values := make([]string, 0, len(fields))
	for _, field := range selectedFields(fields, usageFields) {
		switch field {
		case "harness":
			values = append(values, row.harness)
		case "model":
			values = append(values, row.model)
		case "modelVersion":
			values = append(values, row.modelVersion)
		case "effort":
			values = append(values, row.effort)
		case "role":
			values = append(values, row.role)
		case "repository":
			values = append(values, row.repository)
		}
	}
	return values
}

// passes tells that value is in the filter. An empty filter lets each value pass.
func passes(filter []string, value string) bool {
	return len(filter) == 0 || slices.Contains(filter, value)
}

func distinct(values []string) []string {
	sorted := append([]string{}, values...)
	slices.Sort(sorted)
	return slices.Compact(sorted)
}

// TimeResult is the number of step rows that ended with a result.
type TimeResult struct {
	// Result is "success", or the result that the step row has
	Result string `gork:"result"`
	// Count is the number of rows with this result
	Count int64 `gork:"count"`
}

// TimeStat is the duration statistics of the step rows of one kind or of the task state rows of one state.
type TimeStat struct {
	// Kind is "step" for a row of a step and "state" for a row of a task state
	Kind string `gork:"kind" validate:"oneof=step state"`
	// Name is the kind of the step or the task state
	Name string `gork:"name"`
	// Count is the number of rows
	Count int64 `gork:"count"`
	// TotalSeconds is the sum of the durations of the rows
	TotalSeconds float64 `gork:"totalSeconds"`
	// MeanSeconds is the mean duration of a row
	MeanSeconds float64 `gork:"meanSeconds"`
	// Results are the number of step rows for each result, sorted by result. A task state has none
	Results []TimeResult `gork:"results"`
}

// UsageTimeDay is the time statistics of one group on one day.
type UsageTimeDay struct {
	// Day is the day of the start of the rows, as "YYYY-MM-DD" in the time zone of the request
	Day string `gork:"day"`
	// Group is the value of each selected field, in the order of the fields of the route. With no field it is empty
	Group []string `gork:"group"`
	// Stats are the statistics of each step kind and each task state that has rows, sorted by kind and name
	Stats []TimeStat `gork:"stats"`
}

// UsageTimeGroup is the time statistics of one group in the period.
type UsageTimeGroup struct {
	// Group is the value of each selected field, in the order of the fields of the route. With no field it is empty
	Group []string `gork:"group"`
	// Stats are the statistics of each step kind and each task state that has rows, sorted by kind and name
	Stats []TimeStat `gork:"stats"`
}

// UsageTimes is the time statistics of the steps and the task states in a period.
type UsageTimes struct {
	// Totals are the statistics of all rows that pass the filters
	Totals []TimeStat `gork:"totals"`
	// Days are the statistics of each day and each group, the oldest day first
	Days []UsageTimeDay `gork:"days"`
	// Groups are the statistics of each group
	Groups []UsageTimeGroup `gork:"groups"`
	// Options are the values that each filter can take, before the filters
	Options UsageOptions `gork:"options"`
}

// GetUsageTimesRequest is the request of GetUsageTimes.
type GetUsageTimesRequest struct {
	Query UsageQuery
}

// GetUsageTimesResponse is the response of GetUsageTimes.
type GetUsageTimesResponse struct {
	Body Envelope[UsageTimes]
}

// timeRow is a step row or a task state row. A task state row has no session when its task has no Implementer session.
type timeRow struct {
	kind         string
	name         string
	session      int64
	repository   string
	role         string
	harness      string
	model        string
	modelVersion string
	effort       string
	startedAt    string
	endedAt      string
	result       string
}

// GetUsageTimes returns the durations of the steps and the task states that started in the period.
func (h *handlers) GetUsageTimes(ctx context.Context, req GetUsageTimesRequest) (*GetUsageTimesResponse, error) {
	query := req.Query
	location, err := usageLocation(query.Tz)
	if err != nil {
		return nil, err
	}
	after := query.From.Add(-time.Second).UTC().Format(utcSecond)
	before := query.To.Add(time.Second).UTC().Format(utcSecond)
	steps, err := h.queries.ListStepTimesBetween(ctx, store.ListStepTimesBetweenParams{After: after, Before: before})
	if err != nil {
		return nil, err
	}
	states, err := h.queries.ListTaskStatesBetween(ctx, store.ListTaskStatesBetweenParams{After: after, Before: before})
	if err != nil {
		return nil, err
	}
	reported, err := h.queries.ListReportedModelsBetween(ctx, store.ListReportedModelsBetweenParams{After: after, Before: before})
	if err != nil {
		return nil, err
	}
	modelVersions := map[int64]string{}
	for _, row := range reported {
		modelVersions[row.Session] = row.ReportedModel.String
	}

	rows := make([]timeRow, 0, len(steps)+len(states))
	for _, step := range steps {
		result := step.Result.String
		if !step.Result.Valid {
			result = "success"
		}
		rows = append(rows, timeRow{
			kind: "step", name: step.Kind, session: step.Session.Int64, repository: step.Repository, role: step.Role,
			harness: step.Harness, model: step.Model, modelVersion: modelVersions[step.Session.Int64],
			effort: step.Effort.String, startedAt: step.StartedAt, endedAt: step.EndedAt, result: result,
		})
	}
	for _, state := range states {
		rows = append(rows, timeRow{
			kind: "state", name: state.State, session: state.Session.Int64, repository: state.Repository,
			role: state.Role.String, harness: state.Harness.String, model: state.Model.String,
			modelVersion: modelVersions[state.Session.Int64], effort: state.Effort.String,
			startedAt: state.StartedAt, endedAt: state.EndedAt,
		})
	}

	var (
		options     UsageOptions
		totals      = timeSums{}
		days        = map[usageDayKey]timeSums{}
		groups      = map[groupKey]timeSums{}
		groupValues = map[groupKey][]string{}
	)
	for _, row := range rows {
		started, err := time.Parse(time.RFC3339Nano, row.startedAt)
		if err != nil {
			return nil, err
		}
		if started.Before(query.From) || !started.Before(query.To) {
			continue
		}
		ended, err := time.Parse(time.RFC3339Nano, row.endedAt)
		if err != nil {
			return nil, err
		}
		options.Harnesses = appendNonEmpty(options.Harnesses, row.harness)
		options.Models = appendNonEmpty(options.Models, row.model)
		options.ModelVersions = appendNonEmpty(options.ModelVersions, row.modelVersion)
		options.Efforts = appendNonEmpty(options.Efforts, row.effort)
		options.Roles = appendNonEmpty(options.Roles, row.role)
		options.Repositories = appendNonEmpty(options.Repositories, row.repository)
		if !passes(query.Harness, row.harness) || !passes(query.Model, row.model) ||
			!passes(query.ModelVersion, row.modelVersion) || !passes(query.Effort, row.effort) ||
			!passes(query.Role, row.role) || !passes(query.Repository, row.repository) {
			continue
		}
		group := timeGroupValues(row, query.Group)
		encoded := keyOfGroup(group)
		key := usageDayKey{day: started.In(location).Format(time.DateOnly), group: encoded}
		if days[key] == nil {
			days[key] = timeSums{}
		}
		if groups[encoded] == nil {
			groups[encoded] = timeSums{}
		}
		groupValues[encoded] = group
		seconds := ended.Sub(started).Seconds()
		totals.add(row, seconds)
		days[key].add(row, seconds)
		groups[encoded].add(row, seconds)
	}

	times := UsageTimes{Totals: totals.stats(), Days: []UsageTimeDay{}, Groups: []UsageTimeGroup{}, Options: UsageOptions{
		Harnesses:     distinct(options.Harnesses),
		Models:        distinct(options.Models),
		ModelVersions: distinct(options.ModelVersions),
		Efforts:       distinct(options.Efforts),
		Roles:         distinct(options.Roles),
		Repositories:  distinct(options.Repositories),
	}}
	for key, sums := range days {
		times.Days = append(times.Days, UsageTimeDay{Day: key.day, Group: groupValues[key.group], Stats: sums.stats()})
	}
	slices.SortFunc(times.Days, func(a, b UsageTimeDay) int {
		return cmp.Or(cmp.Compare(a.Day, b.Day), slices.Compare(a.Group, b.Group))
	})
	for group, sums := range groups {
		times.Groups = append(times.Groups, UsageTimeGroup{Group: groupValues[group], Stats: sums.stats()})
	}
	slices.SortFunc(times.Groups, func(a, b UsageTimeGroup) int { return slices.Compare(a.Group, b.Group) })
	return &GetUsageTimesResponse{Body: Envelope[UsageTimes]{Data: times}}, nil
}

type timeStatKey struct {
	kind string
	name string
}

type timeSum struct {
	count   int64
	seconds float64
	results map[string]int64
}

// timeSums adds the rows of a set for each step kind and each task state.
type timeSums map[timeStatKey]*timeSum

func (s timeSums) add(row timeRow, seconds float64) {
	key := timeStatKey{kind: row.kind, name: row.name}
	if s[key] == nil {
		s[key] = &timeSum{results: map[string]int64{}}
	}
	sum := s[key]
	sum.count++
	sum.seconds += seconds
	if row.result != "" {
		sum.results[row.result]++
	}
}

func (s timeSums) stats() []TimeStat {
	stats := []TimeStat{}
	for key, sum := range s {
		stat := TimeStat{
			Kind: key.kind, Name: key.name, Count: sum.count, TotalSeconds: sum.seconds,
			MeanSeconds: sum.seconds / float64(sum.count), Results: []TimeResult{},
		}
		for result, count := range sum.results {
			stat.Results = append(stat.Results, TimeResult{Result: result, Count: count})
		}
		slices.SortFunc(stat.Results, func(a, b TimeResult) int { return cmp.Compare(a.Result, b.Result) })
		stats = append(stats, stat)
	}
	slices.SortFunc(stats, func(a, b TimeStat) int {
		if a.Kind != b.Kind {
			return cmp.Compare(a.Kind, b.Kind)
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return stats
}

func appendNonEmpty(values []string, value string) []string {
	if value == "" {
		return values
	}
	return append(values, value)
}
