package model

import (
	"context"
	"fmt"
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

type Claude5CompatStats struct {
	Start    int64                  `json:"start"`
	End      int64                  `json:"end"`
	Scanned  int                    `json:"scanned"`
	Partial  bool                   `json:"partial"`
	Requests int                    `json:"requests"`
	Rows     []Claude5RuleHit       `json:"rows"`
	Buckets  []Claude5RuleHitBucket `json:"buckets"`
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
	result := &Claude5CompatStats{Start: start, End: end, Rows: []Claude5RuleHit{}, Buckets: []Claude5RuleHitBucket{}}
	tx := LOG_DB.WithContext(ctx).Model(&Log{}).
		Select("created_at", "request_id", "model_name", "other").
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
					Rules []string `json:"rules"`
				} `json:"claude5_compat"`
			} `json:"admin_info"`
		}
		if common.UnmarshalJsonStr(log.Other, &metadata) != nil {
			continue
		}
		for _, rule := range metadata.AdminInfo.Compat.Rules {
			if rule == "" || len(rule) > 128 {
				continue
			}
			key := ruleKey{log.ModelName, rule}
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
