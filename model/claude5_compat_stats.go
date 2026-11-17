package model

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/QuantumNous/new-api/common"
)

type Claude5RuleHit struct {
	Model    string `json:"model"`
	Rule     string `json:"rule"`
	Requests int    `json:"requests"`
}

type Claude5RuleHitBucket struct {
	Start    int64 `json:"start"`
	Requests int   `json:"requests"`
}

type Claude5RuleCounts struct {
	Model     string `json:"model"`
	Rule      string `json:"rule"`
	Evaluated int    `json:"evaluated"`
	Matched   int    `json:"matched"`
	Changed   int    `json:"changed"`
	Skipped   int    `json:"skipped"`
}

type Claude5CohortStats struct {
	Model         string   `json:"model"`
	Region        string   `json:"region"`
	Cohort        string   `json:"cohort"`
	Requests      int      `json:"requests"`
	Successes     int      `json:"successes"`
	Failures      int      `json:"failures"`
	Upstream4xx   int      `json:"upstream_4xx"`
	Upstream5xx   int      `json:"upstream_5xx"`
	UpstreamKnown int      `json:"upstream_known"`
	Retried       int      `json:"retried"`
	Retries       int      `json:"retries"`
	TTFBSamples   int      `json:"ttfb_samples"`
	TTFBP50       *float64 `json:"ttfb_p50_ms"`
	TTFBP95       *float64 `json:"ttfb_p95_ms"`
	Streams       int      `json:"streams"`
	StreamKnown   int      `json:"stream_known"`
	Interrupted   int      `json:"interrupted"`
	SampleMin     int      `json:"sample_min"`
	SampleMax     int      `json:"sample_max"`
}

type Claude5RuleCombination struct {
	Model           string `json:"model"`
	Policy          string `json:"output_config_policy"`
	ThinkingEnabled bool   `json:"thinking_enabled_map"`
	Requests        int    `json:"requests"`
}

type claude5StoredRuleState struct {
	Evaluated bool `json:"rule_evaluated"`
	Matched   bool `json:"rule_matched"`
	Changed   bool `json:"body_changed"`
	Skipped   bool `json:"rule_skipped"`
}

type claude5CohortRequest struct {
	createdAt                               int64
	retry                                   int
	consume                                 bool
	interrupted                             *bool
	stream                                  bool
	ttfb                                    *float64
	upstreamKnown, upstream4xx, upstream5xx bool
	sampleMin, sampleMax                    int
}

type Claude5CompatStats struct {
	RuleStates   []Claude5RuleCounts      `json:"rule_states"`
	Cohorts      []Claude5CohortStats     `json:"cohorts"`
	Combinations []Claude5RuleCombination `json:"combinations"`
	Start        int64                    `json:"start"`
	End          int64                    `json:"end"`
	Scanned      int                      `json:"scanned"`
	Partial      bool                     `json:"partial"`
	Requests     int                      `json:"requests"`
	Rows         []Claude5RuleHit         `json:"rows"`
	Buckets      []Claude5RuleHitBucket   `json:"buckets"`
}

// GetClaude5CompatStats reads existing shared logs only when an administrator
// requests statistics. It adds no counters, writes, or Redis calls to relays.
// Bounds apply to all scanned logs, not only hits, so quiet or disabled
// compatibility settings cannot cause an unbounded search on a busy gateway.
func GetClaude5CompatStats(ctx context.Context, start, end int64) (*Claude5CompatStats, error) {
	if start < 0 || end <= start || end-start > 3600 {
		return nil, fmt.Errorf("invalid compatibility statistics time range")
	}
	const maxRows = 10000
	const maxMetadataBytes = 8 << 20
	result := &Claude5CompatStats{Start: start, End: end, Rows: []Claude5RuleHit{}, Buckets: []Claude5RuleHitBucket{}, RuleStates: []Claude5RuleCounts{}, Cohorts: []Claude5CohortStats{}, Combinations: []Claude5RuleCombination{}}
	tx := LOG_DB.WithContext(ctx).Model(&Log{}).
		Select("created_at", "request_id", "model_name", "other", "type", "is_stream").
		Where("created_at >= ? AND created_at < ? AND type IN ?", start, end, []int{LogTypeConsume, LogTypeError}).
		Order("created_at desc").Limit(maxRows + 1)
	rows, err := tx.Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type ruleKey struct{ model, rule string }
	hits := make(map[ruleKey]map[string]struct{})
	firstHits := make(map[string]int64)
	states := make(map[ruleKey]map[string]claude5StoredRuleState)
	type cohortKey struct{ model, region, cohort string }
	cohorts := make(map[cohortKey]map[string]*claude5CohortRequest)
	type combinationKey struct {
		model, policy string
		thinking      bool
	}
	combinations := make(map[combinationKey]map[string]bool)

	metadataBytes := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if result.Scanned == maxRows {
			result.Partial = true
			break
		}
		var log Log
		if err := LOG_DB.ScanRows(rows, &log); err != nil {
			return nil, err
		}
		metadataBytes += len(log.Other)
		if metadataBytes > maxMetadataBytes {
			result.Partial = true
			break
		}
		result.Scanned++
		if log.RequestId == "" {
			continue
		}
		var metadata struct {
			AdminInfo struct {
				Compat struct {
					Rules          []string                          `json:"rules"`
					Model          string                            `json:"aws_model_id"`
					Region         string                            `json:"region"`
					Cohort         string                            `json:"cohort"`
					RequestHMAC    string                            `json:"request_id_hmac"`
					RuleStates     map[string]claude5StoredRuleState `json:"rule_states"`
					Retry          int                               `json:"retry_count"`
					TTFB           *float64                          `json:"ttfb_ms"`
					Interrupted    *bool                             `json:"stream_interrupted"`
					UpstreamStatus int                               `json:"upstream_status"`
					Sample         *int                              `json:"audit_sample_percent"`
					Policy         string                            `json:"output_config_policy"`
					Thinking       bool                              `json:"thinking_enabled_map"`
				} `json:"claude5_compat"`
			} `json:"admin_info"`
		}
		if common.UnmarshalJsonStr(log.Other, &metadata) != nil {
			continue
		}

		compat := metadata.AdminInfo.Compat
		modelID := log.ModelName
		if compat.Model != "" {
			modelID = compat.Model
		}
		requestKey := log.RequestId
		if len(compat.RequestHMAC) == 64 {
			requestKey = compat.RequestHMAC
		}
		if len(compat.RuleStates) <= 64 {
			for rule, state := range compat.RuleStates {
				if rule == "" || len(rule) > 128 {
					continue
				}
				key := ruleKey{modelID, rule}
				if states[key] == nil {
					states[key] = make(map[string]claude5StoredRuleState)
				}
				old := states[key][requestKey]
				states[key][requestKey] = claude5StoredRuleState{Evaluated: old.Evaluated || state.Evaluated, Matched: old.Matched || state.Matched, Changed: old.Changed || state.Changed, Skipped: old.Skipped || state.Skipped}
			}
		}
		if (compat.Cohort == "compatibility" || compat.Cohort == "control") && compat.Model != "" && compat.Region != "" {
			key := cohortKey{compat.Model, compat.Region, compat.Cohort}
			if cohorts[key] == nil {
				cohorts[key] = make(map[string]*claude5CohortRequest)
			}
			item := cohorts[key][requestKey]
			sample := 100
			if compat.Sample != nil {
				sample = max(0, min(100, *compat.Sample))
			}
			if item == nil {
				item = &claude5CohortRequest{createdAt: -1, sampleMin: sample, sampleMax: sample}
				cohorts[key][requestKey] = item
			}
			item.sampleMin = min(item.sampleMin, sample)
			item.sampleMax = max(item.sampleMax, sample)
			item.upstreamKnown = item.upstreamKnown || (compat.UpstreamStatus >= 100 && compat.UpstreamStatus <= 599)
			item.upstream4xx = item.upstream4xx || (compat.UpstreamStatus >= 400 && compat.UpstreamStatus < 500)
			item.upstream5xx = item.upstream5xx || (compat.UpstreamStatus >= 500 && compat.UpstreamStatus < 600)
			retry := max(0, min(1000, compat.Retry))
			consume := log.Type == LogTypeConsume
			if item.createdAt < 0 || retry > item.retry || (retry == item.retry && (log.CreatedAt > item.createdAt || (log.CreatedAt == item.createdAt && consume))) {
				item.createdAt = log.CreatedAt
				item.retry = retry
				item.consume = consume
				item.interrupted = compat.Interrupted
				item.stream = log.IsStream
				item.ttfb = nil
				if compat.TTFB != nil && !math.IsNaN(*compat.TTFB) && !math.IsInf(*compat.TTFB, 0) && *compat.TTFB >= 0 {
					item.ttfb = compat.TTFB
				}
			}
			if compat.Cohort == "compatibility" {
				key := combinationKey{compat.Model, compat.Policy, compat.Thinking}
				if combinations[key] == nil {
					combinations[key] = make(map[string]bool)
				}
				combinations[key][requestKey] = true
			}
		}
		for _, rule := range metadata.AdminInfo.Compat.Rules {
			if rule == "" || len(rule) > 128 {
				continue
			}
			key := ruleKey{modelID, rule}
			if hits[key] == nil {
				hits[key] = make(map[string]struct{})
			}
			hits[key][log.RequestId] = struct{}{}
			if previous, ok := firstHits[log.RequestId]; !ok || log.CreatedAt < previous {
				firstHits[log.RequestId] = log.CreatedAt
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for key, requests := range states {
		count := Claude5RuleCounts{Model: key.model, Rule: key.rule}
		for _, state := range requests {
			if state.Evaluated {
				count.Evaluated++
			}
			if state.Matched {
				count.Matched++
			}
			if state.Changed {
				count.Changed++
			}
			if state.Skipped {
				count.Skipped++
			}
		}
		result.RuleStates = append(result.RuleStates, count)
	}
	sort.Slice(result.RuleStates, func(i, j int) bool {
		a, b := result.RuleStates[i], result.RuleStates[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Rule < b.Rule
	})
	for key, requests := range cohorts {
		group := Claude5CohortStats{Model: key.model, Region: key.region, Cohort: key.cohort, Requests: len(requests), SampleMin: 100}
		timings := make([]float64, 0, len(requests))
		for _, item := range requests {
			if item.consume && (item.interrupted == nil || !*item.interrupted) {
				group.Successes++
			} else {
				group.Failures++
			}
			if item.upstreamKnown {
				group.UpstreamKnown++
			}
			if item.upstream4xx {
				group.Upstream4xx++
			}
			if item.upstream5xx {
				group.Upstream5xx++
			}
			if item.retry > 0 {
				group.Retried++
				group.Retries += item.retry
			}
			if item.ttfb != nil {
				timings = append(timings, *item.ttfb)
			}
			if item.stream {
				group.Streams++
				if item.interrupted != nil {
					group.StreamKnown++
					if *item.interrupted {
						group.Interrupted++
					}
				}
			}
			group.SampleMin = min(group.SampleMin, item.sampleMin)
			group.SampleMax = max(group.SampleMax, item.sampleMax)
		}
		group.TTFBSamples = len(timings)
		if len(timings) > 0 {
			slices.Sort(timings)
			p50 := timings[(len(timings)-1)/2]
			p95 := timings[(95*len(timings)+99)/100-1]
			group.TTFBP50 = &p50
			group.TTFBP95 = &p95
		}
		result.Cohorts = append(result.Cohorts, group)
	}
	sort.Slice(result.Cohorts, func(i, j int) bool {
		a, b := result.Cohorts[i], result.Cohorts[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		return a.Cohort < b.Cohort
	})
	for key, ids := range combinations {
		result.Combinations = append(result.Combinations, Claude5RuleCombination{Model: key.model, Policy: key.policy, ThinkingEnabled: key.thinking, Requests: len(ids)})
	}
	sort.Slice(result.Combinations, func(i, j int) bool {
		a, b := result.Combinations[i], result.Combinations[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Policy != b.Policy {
			return a.Policy < b.Policy
		}
		return !a.ThinkingEnabled && b.ThinkingEnabled
	})
	result.Requests = len(firstHits)
	for key, ids := range hits {
		result.Rows = append(result.Rows, Claude5RuleHit{Model: key.model, Rule: key.rule, Requests: len(ids)})
	}
	sort.Slice(result.Rows, func(i, j int) bool {
		a, b := result.Rows[i], result.Rows[j]
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Rule < b.Rule
	})
	// A request is assigned to its earliest observed hit, making bucket totals
	// equal unique requests even when retries cross five-minute boundaries.
	for bucketStart := start; bucketStart < end; bucketStart += 300 {
		result.Buckets = append(result.Buckets, Claude5RuleHitBucket{Start: bucketStart})
	}
	for _, timestamp := range firstHits {
		result.Buckets[(timestamp-start)/300].Requests++
	}
	return result, nil
}
