package api

import (
	"cmp"
	"context"
	"database/sql"
	"net/http"
	"slices"
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
	// Group is the value of the group, or empty when the request has no group or the value is not set
	Group  string      `gork:"group"`
	Values UsageValues `gork:"values"`
}

// UsageGroup is the usage of one group in the period.
type UsageGroup struct {
	// Group is the value of the group, or empty when the request has no group or the value is not set
	Group  string      `gork:"group"`
	Values UsageValues `gork:"values"`
}

// UsageOptions are the values that each filter can take in the period.
type UsageOptions struct {
	// Harnesses are the Harnesses of the rows
	Harnesses []string `gork:"harnesses"`
	// Models are the models that the Harness reports, or the models of the config when the Harness reports none
	Models []string `gork:"models"`
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

// GetUsageRequest is the request of GetUsage.
type GetUsageRequest struct {
	Query struct {
		// From is the start of the period. A turn is in the period when it started at From or later
		From time.Time `gork:"from" validate:"required"`
		// To is the end of the period. A turn is in the period when it started before To
		To time.Time `gork:"to" validate:"required"`
		// Tz is the IANA name of the time zone for the days. The default is UTC
		Tz string `gork:"tz"`
		// Harness limits the rows to these Harnesses
		Harness []string `gork:"harness"`
		// Model limits the rows to these models. The model is the model that the Harness reports, or the model of the config when the Harness reports none
		Model []string `gork:"model"`
		// Effort limits the rows to these efforts
		Effort []string `gork:"effort"`
		// Role limits the rows to these roles
		Role []string `gork:"role"`
		// Repository limits the rows to these repositories as "owner/name"
		Repository []string `gork:"repository"`
		// Group is the value that splits the days and the groups. With no Group, all rows are in one group
		Group string `gork:"group" validate:"omitempty,oneof=harness model effort role repository"`
	}
}

// GetUsageResponse is the response of GetUsage.
type GetUsageResponse struct {
	Body Envelope[Usage]
}

// GetUsage returns the tokens and the cost of the turns that started in the period.
func (h *handlers) GetUsage(ctx context.Context, req GetUsageRequest) (*GetUsageResponse, error) {
	query := req.Query
	location := time.UTC
	if query.Tz != "" {
		var err error
		if location, err = time.LoadLocation(query.Tz); err != nil {
			return nil, api.NewHTTPError(http.StatusBadRequest, "The time zone "+query.Tz+" is not an IANA name.")
		}
	}
	rows, err := h.queries.ListTurnUsageBetween(ctx, store.ListTurnUsageBetweenParams{
		After:  query.From.Add(-time.Second).UTC().Format(utcSecond),
		Before: query.To.Add(time.Second).UTC().Format(utcSecond),
	})
	if err != nil {
		return nil, err
	}

	var (
		options UsageOptions
		totals  usageSum
		days    = map[usageDayKey]*usageSum{}
		groups  = map[string]*usageSum{}
	)
	for _, row := range rows {
		started, err := time.Parse(time.RFC3339Nano, row.StartedAt)
		if err != nil {
			return nil, err
		}
		if started.Before(query.From) || !started.Before(query.To) {
			continue
		}
		model := usageModel(row)
		effort := row.Effort.String
		options.Harnesses = append(options.Harnesses, row.Harness)
		options.Models = append(options.Models, model)
		if effort != "" {
			options.Efforts = append(options.Efforts, effort)
		}
		options.Roles = append(options.Roles, row.Role)
		options.Repositories = append(options.Repositories, row.Repository)
		if !passes(query.Harness, row.Harness) || !passes(query.Model, model) || !passes(query.Effort, effort) ||
			!passes(query.Role, row.Role) || !passes(query.Repository, row.Repository) {
			continue
		}
		group := usageGroup(row, query.Group)
		key := usageDayKey{day: started.In(location).Format(time.DateOnly), group: group}
		if days[key] == nil {
			days[key] = &usageSum{}
		}
		if groups[group] == nil {
			groups[group] = &usageSum{}
		}
		totals.add(row)
		days[key].add(row)
		groups[group].add(row)
	}

	usage := Usage{Totals: totals.values(), Days: []UsageDay{}, Groups: []UsageGroup{}, Options: UsageOptions{
		Harnesses:    distinct(options.Harnesses),
		Models:       distinct(options.Models),
		Efforts:      distinct(options.Efforts),
		Roles:        distinct(options.Roles),
		Repositories: distinct(options.Repositories),
	}}
	for key, sum := range days {
		usage.Days = append(usage.Days, UsageDay{Day: key.day, Group: key.group, Values: sum.values()})
	}
	slices.SortFunc(usage.Days, func(a, b UsageDay) int {
		if a.Day != b.Day {
			return cmp.Compare(a.Day, b.Day)
		}
		return cmp.Compare(a.Group, b.Group)
	})
	for group, sum := range groups {
		usage.Groups = append(usage.Groups, UsageGroup{Group: group, Values: sum.values()})
	}
	slices.SortFunc(usage.Groups, func(a, b UsageGroup) int { return cmp.Compare(a.Group, b.Group) })
	return &GetUsageResponse{Body: Envelope[Usage]{Data: usage}}, nil
}

type usageDayKey struct {
	day   string
	group string
}

// usageSum adds the usage rows of a set. A sum stays invalid until a row has a value.
type usageSum struct {
	sessions   map[int64]bool
	input      sql.NullInt64
	output     sql.NullInt64
	cacheRead  sql.NullInt64
	cacheWrite sql.NullInt64
	cost       sql.NullFloat64
}

func (s *usageSum) add(row store.TurnUsage) {
	if s.sessions == nil {
		s.sessions = map[int64]bool{}
	}
	s.sessions[row.Session] = true
	addInt(&s.input, row.InputTokens)
	addInt(&s.output, row.OutputTokens)
	addInt(&s.cacheRead, row.CacheReadTokens)
	addInt(&s.cacheWrite, row.CacheWriteTokens)
	if row.CostUsd.Valid {
		s.cost.Float64 += row.CostUsd.Float64
		s.cost.Valid = true
	}
}

func (s *usageSum) values() UsageValues {
	return UsageValues{
		Sessions:         int64(len(s.sessions)),
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

// usageModel is the model that the Harness reports, or the model of the config when the Harness reports none.
func usageModel(row store.TurnUsage) string {
	if row.ReportedModel.Valid {
		return row.ReportedModel.String
	}
	return row.Model
}

func usageGroup(row store.TurnUsage, group string) string {
	switch group {
	case "harness":
		return row.Harness
	case "model":
		return usageModel(row)
	case "effort":
		return row.Effort.String
	case "role":
		return row.Role
	case "repository":
		return row.Repository
	default:
		return ""
	}
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
