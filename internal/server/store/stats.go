package store

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// StatsReport 是统计页报表（GET /stats）。秒数为小数；比例为 0 到 1，分母为 0 时为空。
type StatsReport struct {
	From        time.Time          `json:"from"`
	To          time.Time          `json:"to"`
	Bucket      string             `json:"bucket"`
	Buckets     []time.Time        `json:"buckets"`
	Tokens      StatsTokens        `json:"tokens"`
	ByOrigin    StatsOrigins       `json:"byOrigin"`
	Wakes       []StatsAgentWakes  `json:"wakes"`
	Context     StatsContext       `json:"context"`
	Questions   StatsQuestions     `json:"questions"`
	Wait        StatsWait          `json:"wait"`
	Outcomes    StatsOutcomes      `json:"outcomes"`
	Agents      []StatsAgentHealth `json:"agents"`
	Tools       StatsTools         `json:"tools"`
	Process     StatsProcess       `json:"process"`
	Concurrency []*float64         `json:"concurrency"`
}

// StatsAgent 是报表中按 Agent 汇总的一行所属的 Agent。
type StatsAgent struct {
	AgentID   uuid.UUID  `json:"agentId"`
	Name      string     `json:"name"`
	ProjectID *uuid.UUID `json:"projectId"`
}

type StatsTokenCounts struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Total      int64 `json:"total"`
}

type StatsTokens struct {
	Totals       StatsTokenCounts   `json:"totals"`
	CacheHitRate *float64           `json:"cacheHitRate"`
	ByModel      []StatsModelTokens `json:"byModel"`
	Series       []int64            `json:"series"`
}

type StatsModelTokens struct {
	Provider *string `json:"provider"`
	Model    *string `json:"model"`
	Total    int64   `json:"total"`
}

// StatsOriginTokens 是按触发来源分的 token。
type StatsOriginTokens struct {
	Console       int64 `json:"console"`
	InboxReminder int64 `json:"inboxReminder"`
	SleepWake     int64 `json:"sleepWake"`
	Task          int64 `json:"task"`
	Other         int64 `json:"other"`
}

func (o *StatsOriginTokens) add(origin string, n int64) {
	switch origin {
	case "console":
		o.Console += n
	case "inboxReminder":
		o.InboxReminder += n
	case "sleepWake":
		o.SleepWake += n
	case "task":
		o.Task += n
	default:
		o.Other += n
	}
}

type StatsOrigins struct {
	Totals           StatsOriginTokens   `json:"totals"`
	UnrequestedShare *float64            `json:"unrequestedShare"`
	Series           []StatsOriginTokens `json:"series"`
}

type StatsAgentWakes struct {
	StatsAgent
	Count              int64    `json:"count"`
	Tokens             int64    `json:"tokens"`
	AvgTokens          float64  `json:"avgTokens"`
	AvgIntervalSeconds *float64 `json:"avgIntervalSeconds"`
}

type StatsContext struct {
	AvgInputPerCall *float64            `json:"avgInputPerCall"`
	Compactions     int64               `json:"compactions"`
	Series          []*float64          `json:"series"`
	Agents          []StatsAgentContext `json:"agents"`
}

type StatsAgentContext struct {
	StatsAgent
	Calls           int64   `json:"calls"`
	AvgInputPerCall float64 `json:"avgInputPerCall"`
	Compactions     int64   `json:"compactions"`
}

type StatsQuestions struct {
	Asked            int64                  `json:"asked"`
	Answered         int64                  `json:"answered"`
	AvgAnswerSeconds *float64               `json:"avgAnswerSeconds"`
	MaxAnswerSeconds *float64               `json:"maxAnswerSeconds"`
	Series           []int64                `json:"series"`
	Pending          []StatsPendingQuestion `json:"pending"`
}

type StatsPendingQuestion struct {
	MessageID uuid.UUID `json:"messageId"`
	StatsAgent
	Text    string    `json:"text"`
	AskedAt time.Time `json:"askedAt"`
}

type StatsWait struct {
	P50         *float64          `json:"p50"`
	P90         *float64          `json:"p90"`
	DurationP50 *float64          `json:"durationP50"`
	Series      []StatsWaitBucket `json:"series"`
}

type StatsWaitBucket struct {
	P50 *float64 `json:"p50"`
	P90 *float64 `json:"p90"`
}

type StatsOutcomeCounts struct {
	Completed   int64 `json:"completed"`
	Interrupted int64 `json:"interrupted"`
	Failed      int64 `json:"failed"`
}

func (c StatsOutcomeCounts) ended() int64 { return c.Completed + c.Interrupted + c.Failed }

type StatsOutcomes struct {
	Totals  StatsOutcomeCounts   `json:"totals"`
	Series  []StatsOutcomeCounts `json:"series"`
	Reasons []StatsOutcomeReason `json:"reasons"`
}

type StatsOutcomeReason struct {
	Kind  string  `json:"kind"`
	Code  *string `json:"code"`
	Count int64   `json:"count"`
}

type StatsAgentHealth struct {
	StatsAgent
	Runs           int64    `json:"runs"`
	CompletionRate *float64 `json:"completionRate"`
	WaitP90        *float64 `json:"waitP90"`
	DurationP50    *float64 `json:"durationP50"`
	Wakes          int64    `json:"wakes"`
	WakeTokens     int64    `json:"wakeTokens"`
	AvgContext     *float64 `json:"avgContext"`
	Compactions    int64    `json:"compactions"`
	Tokens         int64    `json:"tokens"`
	CacheHitRate   *float64 `json:"cacheHitRate"`
}

type StatsTools struct {
	ByName         []StatsTool `json:"byName"`
	CallsPerRunAvg *float64    `json:"callsPerRunAvg"`
	CallsPerRunMax *int64      `json:"callsPerRunMax"`
}

type StatsTool struct {
	Name     string `json:"name"`
	Calls    int64  `json:"calls"`
	Failures int64  `json:"failures"`
}

type StatsProcess struct {
	ModelRetries     int64    `json:"modelRetries"`
	PiExits          int64    `json:"piExits"`
	PiColdStarts     int64    `json:"piColdStarts"`
	PiColdStartAvgMs *float64 `json:"piColdStartAvgMs"`
}

// statsScope 是范围内未删除 Agent 的条件，a 为 agents，$1、$2 为 ScopeFilter.args()。
const statsScope = `a.deleted_at IS NULL AND ($1::uuid IS NULL OR a.project_id = $1) AND (NOT $2 OR a.project_id IS NULL)`

// statsBucket 返回时间 x 所在桶的序号：$3 为 from，$5 为桶宽秒数。
func statsBucket(x string) string {
	return `floor(extract(epoch FROM (` + x + ` - $3::timestamptz)) / $5::float8)::int`
}

// Stats 汇总范围内未删除 Agent 在 [from, to) 内的统计，按 step 分桶（第一桶从 from 开始，最后一桶截止于 to）。
// 用量按记录时间、执行（常态执行与任务轮次，只算派发过的）按排入时间、提问按发出时间计入；
// 并发按服务端本地时间的小时汇总，只观察到现在为止。所有语句在同一可重复读快照中执行。
func (s *Store) Stats(ctx context.Context, f ScopeFilter, from, to time.Time, step time.Duration) (StatsReport, error) {
	now := time.Now()
	pid, platform := f.args()
	n := int((to.Sub(from) + step - 1) / step)
	out := StatsReport{From: from, To: to, Buckets: make([]time.Time, n), Wakes: []StatsAgentWakes{}, Agents: []StatsAgentHealth{}}
	for i := range n {
		out.Buckets[i] = from.Add(time.Duration(i) * step)
	}
	out.Tokens = StatsTokens{ByModel: []StatsModelTokens{}, Series: make([]int64, n)}
	out.ByOrigin.Series = make([]StatsOriginTokens, n)
	out.Context = StatsContext{Series: make([]*float64, n), Agents: []StatsAgentContext{}}
	out.Questions = StatsQuestions{Series: make([]int64, n), Pending: []StatsPendingQuestion{}}
	out.Wait.Series = make([]StatsWaitBucket, n)
	out.Outcomes = StatsOutcomes{Series: make([]StatsOutcomeCounts, n), Reasons: []StatsOutcomeReason{}}
	out.Tools.ByName = []StatsTool{}
	inRange := func(b *int32) bool { return b != nil && *b >= 0 && int(*b) < n }
	args := []any{pid, platform, from, to, step.Seconds()}

	type agentUsage struct {
		input, cacheRead, total, calls, ctx, compactions, wakeTokens int64
	}
	type agentRuns struct {
		count             int64
		outcome           StatsOutcomeCounts
		waitP90, duration *float64
		wakes             int64
		wakeSpan          *float64
	}
	agents := map[uuid.UUID]StatsAgent{}
	usage := map[uuid.UUID]*agentUsage{}
	runs := map[uuid.UUID]*agentRuns{}
	bucketCtx := make([][2]int64, n) // 每桶的调用数与上下文之和
	var totalCalls, totalCtx int64

	err := pgx.BeginTxFunc(ctx, s.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT a.id, a.name, a.project_id FROM agents a WHERE `+statsScope, pid, platform)
		if err != nil {
			return err
		}
		var a StatsAgent
		if _, err := pgx.ForEachRow(rows, []any{&a.AgentID, &a.Name, &a.ProjectID}, func() error { agents[a.AgentID] = a; return nil }); err != nil {
			return err
		}

		// 用量：按（桶、来源）、（提供方、模型）、Agent 与全部四种分组。
		rows, err = tx.Query(ctx, `WITH x AS (
			SELECT u.agent_id, `+statsBucket("u.at")+` AS bucket,
				CASE WHEN r.id IS NOT NULL THEN CASE r.origin WHEN 'console' THEN 'console' WHEN 'inbox_reminder' THEN 'inboxReminder' WHEN 'sleep_wake' THEN 'sleepWake' ELSE 'other' END
					WHEN t.id IS NOT NULL THEN CASE t.origin WHEN 'console_node' THEN 'console' WHEN 'inbox_reminder' THEN 'inboxReminder' WHEN 'sleep_wake' THEN 'sleepWake' ELSE 'task' END
					ELSE 'other' END AS origin,
				coalesce(r.origin, t.origin) = 'sleep_wake' AND coalesce(r.queued_at, t.queued_at) >= $3 AND coalesce(r.queued_at, t.queued_at) < $4 AS wake,
				u.provider, u.model, u.kind, u.input, u.output, u.cache_read, u.cache_write, u.total
			FROM usage_records u JOIN agents a ON a.id = u.agent_id
				LEFT JOIN runs r ON r.id = u.run_id
				LEFT JOIN task_turns t ON t.id = u.task_turn_id
			WHERE `+statsScope+` AND u.at >= $3 AND u.at < $4)
		SELECT grouping(bucket, origin, provider, model, agent_id), bucket, origin, provider, model, agent_id,
			coalesce(sum(input), 0), coalesce(sum(output), 0), coalesce(sum(cache_read), 0), coalesce(sum(cache_write), 0), coalesce(sum(total), 0),
			count(*) FILTER (WHERE kind = 'assistant'), coalesce(sum(input + cache_read) FILTER (WHERE kind = 'assistant'), 0),
			count(*) FILTER (WHERE kind = 'compaction'), coalesce(sum(total) FILTER (WHERE wake), 0)
		FROM x GROUP BY GROUPING SETS ((bucket, origin), (provider, model), (agent_id), ())`, args...)
		if err != nil {
			return err
		}
		var g int32
		var bucket *int32
		var origin, provider, model *string
		var agent *uuid.UUID
		var c StatsTokenCounts
		var calls, ctxSum, compactions, wakeTokens int64
		_, err = pgx.ForEachRow(rows, []any{&g, &bucket, &origin, &provider, &model, &agent,
			&c.Input, &c.Output, &c.CacheRead, &c.CacheWrite, &c.Total, &calls, &ctxSum, &compactions, &wakeTokens}, func() error {
			switch g {
			case 0b00111:
				if inRange(bucket) && origin != nil {
					out.Tokens.Series[*bucket] += c.Total
					out.ByOrigin.Series[*bucket].add(*origin, c.Total)
					out.ByOrigin.Totals.add(*origin, c.Total)
					bucketCtx[*bucket][0] += calls
					bucketCtx[*bucket][1] += ctxSum
				}
			case 0b11001:
				out.Tokens.ByModel = append(out.Tokens.ByModel, StatsModelTokens{Provider: provider, Model: model, Total: c.Total})
			case 0b11110:
				if agent != nil {
					usage[*agent] = &agentUsage{input: c.Input, cacheRead: c.CacheRead, total: c.Total, calls: calls, ctx: ctxSum, compactions: compactions, wakeTokens: wakeTokens}
				}
			case 0b11111:
				out.Tokens.Totals = c
				out.Context.Compactions = compactions
				totalCalls, totalCtx = calls, ctxSum
			}
			return nil
		})
		if err != nil {
			return err
		}

		// 执行：常态执行与派发过的任务轮次，按排入时间计入；按桶、Agent、未完成原因与全部四种分组。
		// 被人停止的常态执行以 endReason user_stopped 结束，任务轮次以 errorCode user_stopped 结束。
		rows, err = tx.Query(ctx, `WITH ex AS (
			SELECT r.agent_id, r.origin, r.status, r.queued_at,
				CASE WHEN r.origin = 'console' THEN extract(epoch FROM (r.running_at - r.queued_at))::float8 END AS wait,
				extract(epoch FROM (r.ended_at - r.running_at))::float8 AS duration,
				CASE r.status WHEN 'failed' THEN 'failed' WHEN 'interrupted' THEN CASE WHEN r.end_reason = 'user_stopped' THEN 'stopped' ELSE 'interrupted' END END AS kind,
				CASE r.status WHEN 'failed' THEN r.error_code WHEN 'interrupted' THEN r.end_reason END AS code
			FROM runs r JOIN agents a ON a.id = r.agent_id
			WHERE `+statsScope+` AND r.started_at IS NOT NULL AND r.queued_at >= $3 AND r.queued_at < $4
			UNION ALL
			SELECT i.agent_id, t.origin, t.status, t.queued_at, NULL,
				extract(epoch FROM (t.ended_at - t.started_at))::float8,
				CASE t.status WHEN 'failed' THEN 'failed' WHEN 'interrupted' THEN CASE WHEN t.error_code = 'user_stopped' THEN 'stopped' ELSE 'interrupted' END END,
				CASE WHEN t.status IN ('failed', 'interrupted') THEN t.error_code END
			FROM task_turns t JOIN task_instances i ON i.id = t.runtime_id JOIN agents a ON a.id = i.agent_id
			WHERE `+statsScope+` AND t.started_at IS NOT NULL AND t.queued_at >= $3 AND t.queued_at < $4),
		x AS (SELECT ex.*, `+statsBucket("ex.queued_at")+` AS bucket FROM ex)
		SELECT grouping(bucket, agent_id, kind, code), bucket, agent_id, kind, code, count(*),
			count(*) FILTER (WHERE status = 'completed'), count(*) FILTER (WHERE status = 'interrupted'), count(*) FILTER (WHERE status = 'failed'),
			percentile_cont(0.5) WITHIN GROUP (ORDER BY wait), percentile_cont(0.9) WITHIN GROUP (ORDER BY wait),
			percentile_cont(0.5) WITHIN GROUP (ORDER BY duration),
			count(*) FILTER (WHERE origin = 'sleep_wake'),
			extract(epoch FROM (max(queued_at) FILTER (WHERE origin = 'sleep_wake') - min(queued_at) FILTER (WHERE origin = 'sleep_wake')))::float8
		FROM x GROUP BY GROUPING SETS ((bucket), (agent_id), (kind, code), ())`, args...)
		if err != nil {
			return err
		}
		var kind, code *string
		var count, wakes int64
		var o StatsOutcomeCounts
		var p50, p90, dur, span *float64
		_, err = pgx.ForEachRow(rows, []any{&g, &bucket, &agent, &kind, &code, &count, &o.Completed, &o.Interrupted, &o.Failed,
			&p50, &p90, &dur, &wakes, &span}, func() error {
			switch g {
			case 0b0111:
				if inRange(bucket) {
					out.Outcomes.Series[*bucket] = o
					out.Wait.Series[*bucket] = StatsWaitBucket{P50: p50, P90: p90}
				}
			case 0b1011:
				if agent != nil {
					runs[*agent] = &agentRuns{count: count, outcome: o, waitP90: p90, duration: dur, wakes: wakes, wakeSpan: span}
				}
			case 0b1100:
				if kind != nil {
					out.Outcomes.Reasons = append(out.Outcomes.Reasons, StatsOutcomeReason{Kind: *kind, Code: code, Count: count})
				}
			case 0b1111:
				out.Outcomes.Totals = o
				out.Wait = StatsWait{P50: p50, P90: p90, DurationP50: dur, Series: out.Wait.Series}
			}
			return nil
		})
		if err != nil {
			return err
		}

		// 提问：Agent 发给控制台的问题，按发出时间计入。
		rows, err = tx.Query(ctx, `SELECT grouping(bucket), bucket, count(*), count(answered), avg(answered), max(answered)
			FROM (SELECT `+statsBucket("m.created_at")+` AS bucket,
					CASE WHEN q.state = 'answered' THEN extract(epoch FROM (q.answered_at - m.created_at))::float8 END AS answered
				FROM inbox_messages m JOIN inbox_questions q ON q.message_id = m.id JOIN agents a ON a.id = m.from_agent_id
				WHERE m.from_kind = 'agent' AND m.to_kind = 'console' AND `+statsScope+` AND m.created_at >= $3 AND m.created_at < $4) x
			GROUP BY GROUPING SETS ((bucket), ())`, args...)
		if err != nil {
			return err
		}
		var answered int64
		var avg, maxSecs *float64
		_, err = pgx.ForEachRow(rows, []any{&g, &bucket, &count, &answered, &avg, &maxSecs}, func() error {
			if g == 1 {
				out.Questions.Asked, out.Questions.Answered, out.Questions.AvgAnswerSeconds, out.Questions.MaxAnswerSeconds = count, answered, avg, maxSecs
			} else if inRange(bucket) {
				out.Questions.Series[*bucket] = count
			}
			return nil
		})
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT m.id, a.id, a.name, a.project_id, left(m.text, 80), m.created_at
			FROM inbox_messages m JOIN inbox_questions q ON q.message_id = m.id JOIN agents a ON a.id = m.from_agent_id
			WHERE m.from_kind = 'agent' AND m.to_kind = 'console' AND `+statsScope+` AND q.state = 'open' AND q.expires_at > $3
			ORDER BY m.created_at, m.id`, pid, platform, now)
		if err != nil {
			return err
		}
		var pq StatsPendingQuestion
		_, err = pgx.ForEachRow(rows, []any{&pq.MessageID, &pq.AgentID, &pq.Name, &pq.ProjectID, &pq.Text, &pq.AskedAt}, func() error {
			out.Questions.Pending = append(out.Questions.Pending, pq)
			return nil
		})
		if err != nil {
			return err
		}

		// 工具：时间窗内排入并开始回复的常态执行；每次调用按（执行、调用 ID）计一次。
		rows, err = tx.Query(ctx, `WITH r AS (
			SELECT r.id FROM runs r JOIN agents a ON a.id = r.agent_id
			WHERE `+statsScope+` AND r.running_at IS NOT NULL AND r.queued_at >= $3 AND r.queued_at < $4),
		c AS (
			SELECT e.run_id, e.tool_call_id, min(e.tool_name) AS name, bool_or(e.state = 'failed') AS failed
			FROM run_tool_events e JOIN r ON r.id = e.run_id GROUP BY e.run_id, e.tool_call_id)
		SELECT name, count(*), count(*) FILTER (WHERE failed), NULL::bigint, NULL::float8, NULL::bigint FROM c GROUP BY name
		UNION ALL
		SELECT NULL, NULL, NULL, count(*), avg(n)::float8, max(n)
		FROM (SELECT r.id, count(c.run_id) AS n FROM r LEFT JOIN c ON c.run_id = r.id GROUP BY r.id) per
		ORDER BY 2 DESC NULLS LAST, 1`, args[:4]...)
		if err != nil {
			return err
		}
		var name *string
		var toolCalls, failures, runCount, maxCalls *int64
		var avgCalls *float64
		_, err = pgx.ForEachRow(rows, []any{&name, &toolCalls, &failures, &runCount, &avgCalls, &maxCalls}, func() error {
			if name != nil {
				out.Tools.ByName = append(out.Tools.ByName, StatsTool{Name: *name, Calls: *toolCalls, Failures: *failures})
			} else {
				out.Tools.CallsPerRunAvg, out.Tools.CallsPerRunMax = avgCalls, maxCalls
			}
			return nil
		})
		if err != nil {
			return err
		}

		// 进程：时间窗内的运行记录事件。
		err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE e.type = 'model.retry_started'),
				count(*) FILTER (WHERE e.type = 'pi.exited' OR (e.type = 'pi.stopped' AND e.data->>'reason' = 'abort_timeout')),
				count(*) FILTER (WHERE e.type = 'pi.started'),
				avg(CASE WHEN jsonb_typeof(e.data->'startupMs') = 'number' THEN (e.data->>'startupMs')::float8 END) FILTER (WHERE e.type = 'pi.started')
			FROM lifecycle_events e JOIN agents a ON a.id = e.agent_id
			WHERE `+statsScope+` AND e.at >= $3 AND e.at < $4 AND e.type IN ('model.retry_started', 'pi.exited', 'pi.stopped', 'pi.started')`, args[:4]...).
			Scan(&out.Process.ModelRetries, &out.Process.PiExits, &out.Process.PiColdStarts, &out.Process.PiColdStartAvgMs)
		if err != nil {
			return err
		}

		// 并发：每段回复按服务端本地整点切成小时段，汇总每段内的回复秒数。
		end := to
		if now.Before(end) {
			end = now
		}
		l := from.In(time.Local)
		align := time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), 0, 0, 0, time.Local)
		if align.After(from) { // 夏令时回拨的重复小时
			align = align.Add(-time.Hour)
		}
		busy := map[int64]float64{}
		if end.After(from) {
			rows, err = tx.Query(ctx, `WITH iv AS (
				SELECT greatest(r.running_at, $3) AS lo, least(coalesce(r.ended_at, $4), $4) AS hi FROM runs r JOIN agents a ON a.id = r.agent_id
				WHERE `+statsScope+` AND r.running_at IS NOT NULL AND r.running_at < $4 AND coalesce(r.ended_at, $4) > $3
				UNION ALL
				SELECT greatest(t.started_at, $3), least(coalesce(t.ended_at, $4), $4)
				FROM task_turns t JOIN task_instances i ON i.id = t.runtime_id JOIN agents a ON a.id = i.agent_id
				WHERE `+statsScope+` AND t.started_at IS NOT NULL AND t.started_at < $4 AND coalesce(t.ended_at, $4) > $3)
			SELECT floor(extract(epoch FROM (s - $5::timestamptz)) / 3600)::bigint,
				sum(extract(epoch FROM (least(s + interval '1 hour', iv.hi) - greatest(s, iv.lo))))::float8
			FROM iv, generate_series($5::timestamptz + floor(extract(epoch FROM (iv.lo - $5::timestamptz)) / 3600) * interval '1 hour', iv.hi - interval '1 microsecond', interval '1 hour') s
			WHERE iv.hi > iv.lo GROUP BY 1`, pid, platform, from, end, align)
			if err != nil {
				return err
			}
			var slot int64
			var secs float64
			if _, err := pgx.ForEachRow(rows, []any{&slot, &secs}, func() error { busy[slot] = secs; return nil }); err != nil {
				return err
			}
		}
		var busySecs, seenSecs [24]float64
		for k := int64(0); ; k++ {
			lo := align.Add(time.Duration(k) * time.Hour)
			if !lo.Before(end) {
				break
			}
			hi := lo.Add(time.Hour)
			if hi.After(end) {
				hi = end
			}
			if lo.Before(from) {
				lo = from
			}
			h := lo.In(time.Local).Hour()
			busySecs[h] += busy[k]
			seenSecs[h] += hi.Sub(lo).Seconds()
		}
		out.Concurrency = make([]*float64, 24)
		for h := range 24 {
			if seenSecs[h] > 0 {
				out.Concurrency[h] = statsRatio(busySecs[h], seenSecs[h])
			}
		}
		return nil
	})
	if err != nil {
		return StatsReport{}, err
	}

	t := out.Tokens.Totals
	out.Tokens.CacheHitRate = statsRatio(float64(t.CacheRead), float64(t.Input+t.CacheRead))
	slices.SortFunc(out.Tokens.ByModel, func(a, b StatsModelTokens) int {
		return cmp.Or(cmp.Compare(b.Total, a.Total), cmpNullLast(a.Provider, b.Provider), cmpNullLast(a.Model, b.Model))
	})
	out.ByOrigin.UnrequestedShare = statsRatio(float64(out.ByOrigin.Totals.SleepWake), float64(t.Total))
	out.Context.AvgInputPerCall = statsRatio(float64(totalCtx), float64(totalCalls))
	for i, b := range bucketCtx {
		out.Context.Series[i] = statsRatio(float64(b[1]), float64(b[0]))
	}
	slices.SortFunc(out.Outcomes.Reasons, func(a, b StatsOutcomeReason) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Kind, b.Kind), cmpNullLast(a.Code, b.Code))
	})

	for id, a := range agents {
		u, r := usage[id], runs[id]
		if u == nil {
			u = &agentUsage{}
		}
		if r == nil {
			r = &agentRuns{}
		}
		if r.wakes > 0 {
			w := StatsAgentWakes{StatsAgent: a, Count: r.wakes, Tokens: u.wakeTokens, AvgTokens: float64(u.wakeTokens) / float64(r.wakes)}
			if r.wakes > 1 && r.wakeSpan != nil {
				w.AvgIntervalSeconds = statsRatio(*r.wakeSpan, float64(r.wakes-1))
			}
			out.Wakes = append(out.Wakes, w)
		}
		avgCtx := statsRatio(float64(u.ctx), float64(u.calls))
		if u.calls > 0 {
			out.Context.Agents = append(out.Context.Agents, StatsAgentContext{StatsAgent: a, Calls: u.calls, AvgInputPerCall: *avgCtx, Compactions: u.compactions})
		}
		if r.count > 0 || u.total > 0 {
			out.Agents = append(out.Agents, StatsAgentHealth{StatsAgent: a, Runs: r.outcome.ended(),
				CompletionRate: statsRatio(float64(r.outcome.Completed), float64(r.outcome.ended())),
				WaitP90:        r.waitP90, DurationP50: r.duration, Wakes: r.wakes, WakeTokens: u.wakeTokens, AvgContext: avgCtx,
				Compactions: u.compactions, Tokens: u.total, CacheHitRate: statsRatio(float64(u.cacheRead), float64(u.input+u.cacheRead))})
		}
	}
	byName := func(a, b StatsAgent) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.AgentID.String(), b.AgentID.String()))
	}
	slices.SortFunc(out.Wakes, func(a, b StatsAgentWakes) int {
		return cmp.Or(cmp.Compare(b.Tokens, a.Tokens), cmp.Compare(b.Count, a.Count), byName(a.StatsAgent, b.StatsAgent))
	})
	slices.SortFunc(out.Context.Agents, func(a, b StatsAgentContext) int {
		return cmp.Or(cmp.Compare(b.AvgInputPerCall, a.AvgInputPerCall), byName(a.StatsAgent, b.StatsAgent))
	})
	slices.SortFunc(out.Agents, func(a, b StatsAgentHealth) int {
		return cmp.Or(cmp.Compare(b.Tokens, a.Tokens), cmp.Compare(b.Runs, a.Runs), byName(a.StatsAgent, b.StatsAgent))
	})
	return out, nil
}

// statsRatio 返回 n ÷ d；d 为 0 时为空。
func statsRatio(n, d float64) *float64 {
	if d == 0 {
		return nil
	}
	v := n / d
	return &v
}

// cmpNullLast 比较两个可空字符串，空值排在后面。
func cmpNullLast(a, b *string) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return cmp.Compare(*a, *b)
}
