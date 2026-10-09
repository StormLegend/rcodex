package store

import "encoding/json"

type TokenCounts struct {
	Total     int64 `json:"totalTokens"`
	Input     int64 `json:"inputTokens"`
	Cached    int64 `json:"cachedInputTokens"`
	Output    int64 `json:"outputTokens"`
	Reasoning int64 `json:"reasoningOutputTokens"`
}
type Usage struct {
	TurnID        string      `json:"turn_id"`
	Tokens        TokenCounts `json:"tokens"`
	ContextTokens int64       `json:"context_tokens"`
	ContextWindow int64       `json:"context_window"`
	Updated       int64       `json:"updated"`
	Source        string      `json:"source"`
}

func (a TokenCounts) sub(b TokenCounts) TokenCounts {
	return TokenCounts{a.Total - b.Total, a.Input - b.Input, a.Cached - b.Cached, a.Output - b.Output, a.Reasoning - b.Reasoning}
}
func (a *TokenCounts) add(b TokenCounts) {
	a.Total += b.Total
	a.Input += b.Input
	a.Cached += b.Cached
	a.Output += b.Output
	a.Reasoning += b.Reasoning
}

// TurnUsage uses provider telemetry, not text-length estimates. Codex "total"
// spans the entire native thread; "last" is just the latest model request.
// Count cumulative deltas within this gateway turn, handling counter resets
// (e.g. compaction) and duplicate snapshots without double-counting.
func (s *Store) TurnUsage(session, turn string) (*Usage, error) {
	rows, e := s.DB.Query(`SELECT kind,data,created FROM events WHERE session_id=? AND turn_id=? AND kind IN ('thread/tokenUsage/updated','claude/event') ORDER BY id`, session, turn)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out *Usage
	var prev TokenCounts
	seen := false
	type claudeTokens struct {
		Input  int64 `json:"input_tokens"`
		Output int64 `json:"output_tokens"`
		Read   int64 `json:"cache_read_input_tokens"`
		Write  int64 `json:"cache_creation_input_tokens"`
	}
	counts := func(v claudeTokens) TokenCounts {
		in := v.Input + v.Read + v.Write
		return TokenCounts{Total: in + v.Output, Input: in, Cached: v.Read, Output: v.Output}
	}
	messages := map[string]TokenCounts{}
	for rows.Next() {
		var kind string
		var data []byte
		var created int64
		if e = rows.Scan(&kind, &data, &created); e != nil {
			return nil, e
		}
		if kind == "thread/tokenUsage/updated" {
			var v struct {
				TokenUsage *struct {
					Total, Last        TokenCounts
					ModelContextWindow int64
				}
			}
			if json.Unmarshal(data, &v) != nil || v.TokenUsage == nil {
				continue
			}
			u := v.TokenUsage
			if out == nil {
				out = &Usage{TurnID: turn, Source: "codex"}
			}
			delta := u.Last
			if seen {
				delta = u.Total.sub(prev)
				if delta.Total < 0 || delta.Input < 0 || delta.Output < 0 {
					delta = u.Last
				}
			}
			out.Tokens.add(delta)
			out.ContextTokens = u.Last.Total
			out.ContextWindow = u.ModelContextWindow
			out.Updated = created
			prev = u.Total
			seen = true
		} else {
			var v struct {
				Type    string
				Usage   *claudeTokens
				Message struct {
					ID    string
					Usage *claudeTokens
				}
				ModelUsage map[string]struct {
					ContextWindow int64 `json:"contextWindow"`
				}
			}
			if json.Unmarshal(data, &v) != nil {
				continue
			}
			if v.Type == "assistant" && v.Message.Usage != nil {
				if out == nil {
					out = &Usage{TurnID: turn, Source: "claude"}
				}
				c := counts(*v.Message.Usage)
				out.Tokens.add(c.sub(messages[v.Message.ID]))
				messages[v.Message.ID] = c
				out.ContextTokens = c.Total
				out.Updated = created
			}
			if v.Type == "result" && v.Usage != nil {
				if out == nil {
					out = &Usage{TurnID: turn, Source: "claude"}
				}
				out.Tokens = counts(*v.Usage)
				out.Updated = created
				// Multiple models have different windows. Do not invent one ratio.
				if len(v.ModelUsage) == 1 {
					for _, m := range v.ModelUsage {
						out.ContextWindow = m.ContextWindow
					}
				}
			}
		}
	}
	return out, rows.Err()
}
