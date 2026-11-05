package store

import (
	"cmp"
	"context"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
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
const statsScope = `a.deleted_at IS NULL AND ($1 IS NULL OR a.project_id = $1) AND (NOT $2 OR a.project_id IS NULL)`

// statsBucket 返回时间 x 所在桶的序号：$3 为 from，$5 为桶宽微秒；x 不早于 from。
func statsBucket(x string) string {
	return `((` + x + `) - $3) / $5`
}

// statsRuns 汇总一组执行：结果计数、等待与回复时长（秒）、唤醒次数与首末唤醒的排入时间。
type statsRuns struct {
	count               int64
	outcome             StatsOutcomeCounts
	waits, durations    []float64
	wakes               int64
	firstWake, lastWake time.Time
}

func (r *statsRuns) add(status, origin string, queued time.Time, wait, duration *float64) {
	r.count++
	switch status {
	case "completed":
		r.outcome.Completed++
	case "interrupted":
		r.outcome.Interrupted++
	case "failed":
		r.outcome.Failed++
	}
	if wait != nil {
		r.waits = append(r.waits, *wait)
	}
	if duration != nil {
		r.durations = append(r.durations, *duration)
	}
	if origin == "sleep_wake" {
		if r.wakes == 0 || queued.Before(r.firstWake) {
			r.firstWake = queued
		}
		if r.wakes == 0 || queued.After(r.lastWake) {
			r.lastWake = queued
		}
		r.wakes++
	}
}

// wakeSpan 是首末两次唤醒排入的间隔秒数；没有唤醒时为空。
func (r *statsRuns) wakeSpan() *float64 {
	if r.wakes == 0 {
		return nil
	}
	v := r.lastWake.Sub(r.firstWake).Seconds()
	return &v
}

// Stats 汇总范围内未删除 Agent 在 [from, to) 内的统计，按 step 分桶（第一桶从 from 开始，最后一桶截止于 to）。
// 用量按记录时间、执行（常态执行与任务轮次，只算派发过的）按排入时间、提问按发出时间计入；
// 并发按服务端本地时间的小时汇总，只观察到现在为止。所有语句在同一只读事务中执行：每个库各是一个快照，
// 运行记录事件与工具事件在 events.db，它的快照与 data.db 的可能相差几个事务，报表不受影响。
// 各种分组与分位数在 Go 里汇总：SQL 只按最细的组合分组，或返回逐条执行。
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
	inRange := func(b int) bool { return b >= 0 && b < n }
	args := []any{pid, platform, from, to, step.Microseconds()}

	type agentUsage struct {
		input, cacheRead, total, calls, ctx, compactions, wakeTokens int64
	}
	agents := map[uuid.UUID]StatsAgent{}
	usage := map[uuid.UUID]*agentUsage{}
	runs := map[uuid.UUID]*statsRuns{}
	bucketCtx := make([][2]int64, n) // 每桶的调用数与上下文之和
	var totalCalls, totalCtx int64

	err := s.Pool.ReadTx(ctx, func(tx *db.Tx) error {
		rows, err := tx.Query(ctx, `SELECT a.id, a.name, a.project_id FROM agents a WHERE `+statsScope, pid, platform)
		if err != nil {
			return err
		}
		var a StatsAgent
		if _, err := db.ForEachRow(rows, []any{&a.AgentID, &a.Name, &a.ProjectID}, func() error { agents[a.AgentID] = a; return nil }); err != nil {
			return err
		}

		// 用量：按（桶、来源、提供方、模型、Agent）分组，再在 Go 里汇总成按桶与来源、按模型、按 Agent 与全部。
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
		SELECT bucket, origin, provider, model, agent_id,
			coalesce(sum(input), 0), coalesce(sum(output), 0), coalesce(sum(cache_read), 0), coalesce(sum(cache_write), 0), coalesce(sum(total), 0),
			count(*) FILTER (WHERE kind = 'assistant'), coalesce(sum(input + cache_read) FILTER (WHERE kind = 'assistant'), 0),
			count(*) FILTER (WHERE kind = 'compaction'), coalesce(sum(total) FILTER (WHERE wake), 0)
		FROM x GROUP BY bucket, origin, provider, model, agent_id`, args...)
		if err != nil {
			return err
		}
		type modelKey struct {
			provider, model       string
			hasProvider, hasModel bool
		}
		byModel := map[modelKey]int64{}
		var bucket int
		var origin string
		var provider, model *string
		var agent uuid.UUID
		var c StatsTokenCounts
		var calls, ctxSum, compactions, wakeTokens int64
		_, err = db.ForEachRow(rows, []any{&bucket, &origin, &provider, &model, &agent,
			&c.Input, &c.Output, &c.CacheRead, &c.CacheWrite, &c.Total, &calls, &ctxSum, &compactions, &wakeTokens}, func() error {
			if inRange(bucket) {
				out.Tokens.Series[bucket] += c.Total
				out.ByOrigin.Series[bucket].add(origin, c.Total)
				out.ByOrigin.Totals.add(origin, c.Total)
				bucketCtx[bucket][0] += calls
				bucketCtx[bucket][1] += ctxSum
			}
			k := modelKey{hasProvider: provider != nil, hasModel: model != nil}
			if provider != nil {
				k.provider = *provider
			}
			if model != nil {
				k.model = *model
			}
			byModel[k] += c.Total
			u := usage[agent]
			if u == nil {
				u = &agentUsage{}
				usage[agent] = u
			}
			u.input += c.Input
			u.cacheRead += c.CacheRead
			u.total += c.Total
			u.calls += calls
			u.ctx += ctxSum
			u.compactions += compactions
			u.wakeTokens += wakeTokens
			t := &out.Tokens.Totals
			t.Input, t.Output, t.CacheRead, t.CacheWrite, t.Total = t.Input+c.Input, t.Output+c.Output, t.CacheRead+c.CacheRead, t.CacheWrite+c.CacheWrite, t.Total+c.Total
			out.Context.Compactions += compactions
			totalCalls += calls
			totalCtx += ctxSum
			return nil
		})
		if err != nil {
			return err
		}
		for k, total := range byModel {
			m := StatsModelTokens{Total: total}
			if k.hasProvider {
				m.Provider = &k.provider
			}
			if k.hasModel {
				m.Model = &k.model
			}
			out.Tokens.ByModel = append(out.Tokens.ByModel, m)
		}

		// 执行：常态执行与派发过的任务轮次，按排入时间计入；逐条读出，在 Go 里按桶、Agent、未完成原因与全部汇总。
		// 被人停止的常态执行以 endReason user_stopped 结束，任务轮次以 errorCode user_stopped 结束。
		rows, err = tx.Query(ctx, `SELECT r.agent_id, r.origin, r.status, r.queued_at,
				CASE WHEN r.origin = 'console' THEN (r.running_at - r.queued_at) / 1e6 END,
				(r.ended_at - r.running_at) / 1e6,
				CASE r.status WHEN 'failed' THEN 'failed' WHEN 'interrupted' THEN CASE WHEN r.end_reason = 'user_stopped' THEN 'stopped' ELSE 'interrupted' END END,
				CASE r.status WHEN 'failed' THEN r.error_code WHEN 'interrupted' THEN r.end_reason END
			FROM runs r JOIN agents a ON a.id = r.agent_id
			WHERE `+statsScope+` AND r.started_at IS NOT NULL AND r.queued_at >= $3 AND r.queued_at < $4
			UNION ALL
			SELECT i.agent_id, t.origin, t.status, t.queued_at, NULL,
				(t.ended_at - t.started_at) / 1e6,
				CASE t.status WHEN 'failed' THEN 'failed' WHEN 'interrupted' THEN CASE WHEN t.error_code = 'user_stopped' THEN 'stopped' ELSE 'interrupted' END END,
				CASE WHEN t.status IN ('failed', 'interrupted') THEN t.error_code END
			FROM task_turns t JOIN task_instances i ON i.id = t.runtime_id JOIN agents a ON a.id = i.agent_id
			WHERE `+statsScope+` AND t.started_at IS NOT NULL AND t.queued_at >= $3 AND t.queued_at < $4`, args[:4]...)
		if err != nil {
			return err
		}
		type reasonKey struct {
			kind, code string
			hasCode    bool
		}
		var all statsRuns
		perBucket := make([]statsRuns, n)
		reasons := map[reasonKey]int64{}
		var status string
		var queued time.Time
		var wait, duration *float64
		var kind, code *string
		_, err = db.ForEachRow(rows, []any{&agent, &origin, &status, &queued, &wait, &duration, &kind, &code}, func() error {
			all.add(status, origin, queued, wait, duration)
			if b := int(queued.Sub(from) / step); inRange(b) {
				perBucket[b].add(status, origin, queued, wait, duration)
			}
			r := runs[agent]
			if r == nil {
				r = &statsRuns{}
				runs[agent] = r
			}
			r.add(status, origin, queued, wait, duration)
			if kind != nil {
				k := reasonKey{kind: *kind, hasCode: code != nil}
				if code != nil {
					k.code = *code
				}
				reasons[k]++
			}
			return nil
		})
		if err != nil {
			return err
		}
		for i := range perBucket {
			b := &perBucket[i]
			out.Outcomes.Series[i] = b.outcome
			out.Wait.Series[i] = StatsWaitBucket{P50: statsPercentile(b.waits, 0.5), P90: statsPercentile(b.waits, 0.9)}
		}
		for k, count := range reasons {
			r := StatsOutcomeReason{Kind: k.kind, Count: count}
			if k.hasCode {
				r.Code = &k.code
			}
			out.Outcomes.Reasons = append(out.Outcomes.Reasons, r)
		}
		out.Outcomes.Totals = all.outcome
		out.Wait = StatsWait{P50: statsPercentile(all.waits, 0.5), P90: statsPercentile(all.waits, 0.9), DurationP50: statsPercentile(all.durations, 0.5), Series: out.Wait.Series}

		// 提问：Agent 发给控制台的问题，按发出时间计入；按桶分组，全部由各桶相加。
		rows, err = tx.Query(ctx, `SELECT bucket, count(*), count(answered), sum(answered), max(answered)
			FROM (SELECT `+statsBucket("m.created_at")+` AS bucket,
					CASE WHEN q.state = 'answered' THEN (q.answered_at - m.created_at) / 1e6 END AS answered
				FROM inbox_messages m JOIN inbox_questions q ON q.message_id = m.id JOIN agents a ON a.id = m.from_agent_id
				WHERE m.from_kind = 'agent' AND m.to_kind = 'console' AND `+statsScope+` AND m.created_at >= $3 AND m.created_at < $4) x
			GROUP BY bucket`, args...)
		if err != nil {
			return err
		}
		var count, answered int64
		var sumSecs, maxSecs *float64
		var answeredSecs float64
		_, err = db.ForEachRow(rows, []any{&bucket, &count, &answered, &sumSecs, &maxSecs}, func() error {
			if inRange(bucket) {
				out.Questions.Series[bucket] = count
			}
			out.Questions.Asked += count
			out.Questions.Answered += answered
			if sumSecs != nil {
				answeredSecs += *sumSecs
			}
			if maxSecs != nil && (out.Questions.MaxAnswerSeconds == nil || *maxSecs > *out.Questions.MaxAnswerSeconds) {
				v := *maxSecs
				out.Questions.MaxAnswerSeconds = &v
			}
			return nil
		})
		if err != nil {
			return err
		}
		out.Questions.AvgAnswerSeconds = statsRatio(answeredSecs, float64(out.Questions.Answered))
		rows, err = tx.Query(ctx, `SELECT m.id, a.id, a.name, a.project_id, substr(m.text, 1, 80), m.created_at
			FROM inbox_messages m JOIN inbox_questions q ON q.message_id = m.id JOIN agents a ON a.id = m.from_agent_id
			WHERE m.from_kind = 'agent' AND m.to_kind = 'console' AND `+statsScope+` AND q.state = 'open' AND q.expires_at > $3
			ORDER BY m.created_at, m.id`, pid, platform, now)
		if err != nil {
			return err
		}
		var pq StatsPendingQuestion
		_, err = db.ForEachRow(rows, []any{&pq.MessageID, &pq.AgentID, &pq.Name, &pq.ProjectID, &pq.Text, &pq.AskedAt}, func() error {
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
			SELECT e.run_id, e.tool_call_id, min(e.tool_name) AS name, max(e.state = 'failed') AS failed
			FROM run_tool_events e JOIN r ON r.id = e.run_id GROUP BY e.run_id, e.tool_call_id)
		SELECT name, count(*), count(*) FILTER (WHERE failed), CAST(NULL AS INTEGER), CAST(NULL AS REAL), CAST(NULL AS INTEGER) FROM c GROUP BY name
		UNION ALL
		SELECT NULL, NULL, NULL, count(*), CAST(avg(n) AS REAL), max(n)
		FROM (SELECT r.id, count(c.run_id) AS n FROM r LEFT JOIN c ON c.run_id = r.id GROUP BY r.id) per
		ORDER BY 2 DESC NULLS LAST, 1`, args[:4]...)
		if err != nil {
			return err
		}
		var name *string
		var toolCalls, failures, runCount, maxCalls *int64
		var avgCalls *float64
		_, err = db.ForEachRow(rows, []any{&name, &toolCalls, &failures, &runCount, &avgCalls, &maxCalls}, func() error {
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
				count(*) FILTER (WHERE e.type = 'pi.exited' OR (e.type = 'pi.stopped' AND e.data ->> 'reason' = 'abort_timeout')),
				count(*) FILTER (WHERE e.type = 'pi.started'),
				avg(CASE WHEN json_type(e.data, '$.startupMs') IN ('integer', 'real') THEN CAST(e.data ->> 'startupMs' AS REAL) END) FILTER (WHERE e.type = 'pi.started')
			FROM lifecycle_events e JOIN agents a ON a.id = e.agent_id
			WHERE `+statsScope+` AND e.at >= $3 AND e.at < $4 AND e.type IN ('model.retry_started', 'pi.exited', 'pi.stopped', 'pi.started')`, args[:4]...).
			Scan(&out.Process.ModelRetries, &out.Process.PiExits, &out.Process.PiColdStarts, &out.Process.PiColdStartAvgMs)
		if err != nil {
			return err
		}

		// 并发：每段回复按服务端本地整点切成小时段，汇总每段内的回复秒数。SQL 读出截在窗口内的回复区间（微秒），切段在 Go 里做。
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
			rows, err = tx.Query(ctx, `SELECT max(r.running_at, $3), min(coalesce(r.ended_at, $4), $4) FROM runs r JOIN agents a ON a.id = r.agent_id
				WHERE `+statsScope+` AND r.running_at IS NOT NULL AND r.running_at < $4 AND coalesce(r.ended_at, $4) > $3
				UNION ALL
				SELECT max(t.started_at, $3), min(coalesce(t.ended_at, $4), $4)
				FROM task_turns t JOIN task_instances i ON i.id = t.runtime_id JOIN agents a ON a.id = i.agent_id
				WHERE `+statsScope+` AND t.started_at IS NOT NULL AND t.started_at < $4 AND coalesce(t.ended_at, $4) > $3`, pid, platform, from, end)
			if err != nil {
				return err
			}
			const hour = int64(time.Hour / time.Microsecond)
			base := align.UnixMicro()
			var lo, hi int64
			_, err = db.ForEachRow(rows, []any{&lo, &hi}, func() error {
				// lo 不早于 from，因此不早于 align。
				for slot := (lo - base) / hour; base+slot*hour < hi; slot++ {
					s := base + slot*hour
					busy[slot] += float64(min(s+hour, hi)-max(s, lo)) / 1e6
				}
				return nil
			})
			if err != nil {
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
			r = &statsRuns{}
		}
		if r.wakes > 0 {
			w := StatsAgentWakes{StatsAgent: a, Count: r.wakes, Tokens: u.wakeTokens, AvgTokens: float64(u.wakeTokens) / float64(r.wakes)}
			if span := r.wakeSpan(); r.wakes > 1 && span != nil {
				w.AvgIntervalSeconds = statsRatio(*span, float64(r.wakes-1))
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
				WaitP90:        statsPercentile(r.waits, 0.9), DurationP50: statsPercentile(r.durations, 0.5), Wakes: r.wakes, WakeTokens: u.wakeTokens, AvgContext: avgCtx,
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

// statsPercentile 按 PostgreSQL percentile_cont 的线性插值返回 vals 的 p 分位（会排序 vals）；vals 为空时为空。
func statsPercentile(vals []float64, p float64) *float64 {
	if len(vals) == 0 {
		return nil
	}
	slices.Sort(vals)
	pos := p * float64(len(vals)-1)
	lo, hi := int(math.Floor(pos)), int(math.Ceil(pos))
	v := vals[lo] + (vals[hi]-vals[lo])*(pos-float64(lo))
	return &v
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
