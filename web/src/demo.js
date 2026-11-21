const dayMs = 86_400_000;
const offset = 8 * 3_600_000;
const usd = 1_000_000_000;
const shanghaiDay = time => new Date(time + offset).toISOString().slice(0, 10);
const round = (n, places = 1) => Number(n.toFixed(places));
const sum = (rows, key) => rows.reduce((n, row) => n + (row[key] || 0), 0);

const models = [
  { model: 'gpt-5', protocol: 'responses', input: 1250, output: 10000, cached: 125, cache_write: 0, max_input: 128000, max_output: 8192 },
  { model: 'claude-sonnet-4', protocol: 'messages', input: 3000, output: 15000, cached: 300, cache_write: 3750, max_input: 200000, max_output: 8192 },
  { model: 'gemini-2.5-flash', protocol: 'gemini', input: 300, output: 2500, cached: 30, cache_write: 0, max_input: 128000, max_output: 8192 }
];
const policies = [
  { id: 'demo-team', masked_key: 'sk-demo-••••team', cpa_present: true, name: '开发团队', enabled: true, daily: 3 * usd, total: 40 * usd, in_flight: 80_000_000, reserved_unresolved: 0 },
  { id: 'demo-jobs', masked_key: 'sk-demo-••••jobs', cpa_present: true, name: '自动化任务', enabled: true, daily: usd, total: 15 * usd, in_flight: 0, reserved_unresolved: 36_000_000 },
  { id: 'demo-lab', masked_key: 'sk-demo-••••lab', cpa_present: false, name: '测试沙盒', enabled: false, daily: 500_000_000, total: 5 * usd, in_flight: 0, reserved_unresolved: 0 }
];

function group(rows, key) {
  const result = new Map();
  for (const row of rows) {
    const id = key(row);
    if (!result.has(id)) result.set(id, []);
    result.get(id).push(row);
  }
  return [...result.values()];
}

function totals(rows) {
  const pending = rows.filter(row => row.cost === null).length;
  return {
    requests: rows.length,
    input: sum(rows, 'input'),
    output: sum(rows, 'output'),
    cached: sum(rows, 'cached'),
    cache_write: sum(rows, 'cache_write'),
    total: sum(rows, 'total'),
    cost: pending === rows.length ? null : sum(rows, 'cost'),
    failures: sum(rows, 'failed'),
    pending
  };
}

function event(id, happened, keyIndex, modelIndex, sequence, failed = false, pending = false) {
  const rate = models[modelIndex];
  const input = failed ? 0 : 1800 + (sequence * 97) % 5300;
  const output = failed ? 0 : 120 + (sequence * 29) % 850;
  const cached = failed ? 0 : 350 + (sequence * 113) % 9400;
  const write = failed || modelIndex !== 1 ? 0 : 200 + (sequence * 43) % 800;
  return {
    id, happened, key_id: policies[keyIndex].id, model: rate.model,
    input, output, cached, cache_write: write, total: input + output + cached + write,
    cost: pending ? null : input * rate.input + output * rate.output + cached * rate.cached + write * rate.cache_write,
    failed: Number(failed), latency: failed ? 0 : 690 + (sequence * 149) % 3420,
    ttft: failed ? 0 : 190 + (sequence * 31) % 420, blocked: null
  };
}

const conversations = [
  {
    id: '审计报表接口', key: 0, model: 0,
    messages: [
      ['帮我看一下报表接口的设计，数据量大时分页有什么问题？', '当前分页按 offset 扫描。建议用 created_at 和 id 作为联合游标，避免翻到深页时重复扫描。'],
      ['看一下现有查询，给我一个改造草案。', '查询已经按创建时间排序。把 id 加为第二排序项，并在下一页条件中比较 (created_at, id)。', { id: 'demo-tool-query', name: 'read_query', arguments: '{"name":"report_page"}', result: 'SELECT id, created_at FROM report ORDER BY created_at DESC LIMIT 50 OFFSET 0' }],
      ['再补一个验证场景。', '在读取第一页后插入一条新记录，使用旧游标读取第二页，检查两页没有重叠且顺序稳定。']
    ]
  },
  {
    id: '每日报表检查', key: 1, model: 1,
    messages: [
      ['汇总今天的报表任务，哪些步骤需要重试？', '三个步骤已完成。邮件摘要生成需要重试，导出文件已经可用。'],
      ['给一条适合贴到值班记录的摘要。', '报表已生成，摘要待重试。复核时请检查任务编号和最终发送状态。']
    ]
  },
  {
    id: '文档检索预览', key: 0, model: 2,
    messages: [
      ['从这份文档找一下归档正文保留多久。', '示例配置保留正文 14 天。到期后仍可查看账单和会话索引。'],
      ['把结论压缩到一句话。', '正文保留 14 天，消费记录和会话索引继续可查。']
    ]
  }
];

function base64JSON(value) {
  return btoa(String.fromCharCode(...new TextEncoder().encode(JSON.stringify(value))));
}

export function createDemo(now = Date.now()) {
  const today = Math.floor((now + offset) / dayMs) * dayMs - offset;
  const counts = [7, 9, 12, 10, 13, 17, 8, 14, 15, 10, 17, 21, 14, 11, 18, 24, 21, 12, 16, 23, 26, 18, 16, 25, 30, 24, 19, 26, 29, 24];
  const events = counts.flatMap((count, day) => {
    const age = counts.length - 1 - day;
    const start = today - age * dayMs;
    const elapsed = age ? 22 * 3_600_000 : Math.max(1, now - start);
    return Array.from({length: count}, (_, index) => {
      const sequence = day * 31 + index;
      const keyChoice = (sequence * 3) % 7;
      const keyIndex = keyChoice < 4 ? 0 : keyChoice < 6 || !age ? 1 : 2;
      const modelIndex = (sequence + (keyIndex || day)) % models.length;
      return event('demo-event-' + sequence, start + Math.floor(elapsed * (index + 1) / (count + 1)),
        keyIndex, modelIndex, sequence, sequence % 43 === 0, sequence % 57 === 8);
    });
  });

  const byTurn = new Map();
  for (const [sessionIndex, session] of conversations.entries()) {
    const history = [];
    for (const [index, [prompt, answer, tool]] of session.messages.entries()) {
      const id = 'demo-turn-' + sessionIndex + '-' + index;
      const happened = now - (sessionIndex * 70 + (session.messages.length - index) * 4) * 60_000;
      const row = event('demo-linked-' + id, happened, session.key, session.model, 920 + sessionIndex * 20 + index);
      row.turn_id = id;
      const turn = {...row, id, session: session.id, started: happened, status: 200, settled: 1, audit_state: 'complete'};
      history.push({role: 'user', content: prompt});
      const request = {model: row.model, messages: history.slice()};
      if (tool) {
        request.messages.push({role: 'tool', tool_call_id: tool.id, content: tool.result});
      }
      const tools = tool ? [{id: tool.id, name: tool.name, arguments: tool.arguments}] : [];
      const body = {
        request_raw: base64JSON(request),
        response_raw: base64JSON({type: 'response.completed', output_text: answer, tools}),
        output: {text: answer, tools}
      };
      history.push({role: 'assistant', content: answer});
      byTurn.set(id, {turn, body});
      events.push(row);
    }
  }
  events.sort((a, b) => b.happened - a.happened);
  const turns = [...byTurn.values()].map(row => row.turn).sort((a, b) => b.started - a.started);
  const keys = policies.map(policy => {
    const all = events.filter(row => row.key_id === policy.id);
    const spent = sum(all, 'cost');
    const spentToday = sum(all.filter(row => row.happened >= today), 'cost');
    const available = Math.min(policy.daily - spentToday, policy.total - spent) - policy.in_flight - policy.reserved_unresolved;
    return {...policy, spent, spent_today: spentToday, admission_capacity: policy.enabled ? Math.max(0, available) : 0};
  });
  const prices = models.map((rate, index) => ({...rate, id: index + 1, billed_model: rate.model, verified: false, active: true, is_reference: true, source: "演示参考价格", source_url: "https://example.com/reference-pricing", source_date: "2026-11-21"}));

  return path => {
    const [resource, query] = path.split('?', 2);
    const params = new URLSearchParams(query);
    const requested = Number(params.get('days'));
    const days = [7, 14, 30].includes(requested) ? requested : 30;
    if (resource === 'overview' || resource === 'series') {
      const rows = events.filter(row => row.happened >= today - (days - 1) * dayMs);
      if (resource === 'series') {
        return group(rows, row => shanghaiDay(row.happened)).map(rows => {
          const aggregate = totals(rows);
          return {...aggregate, day: shanghaiDay(rows[0].happened), tokens: aggregate.total,
            input_tokens: aggregate.input + aggregate.cached + aggregate.cache_write};
        }).reverse();
      }
      return group(rows, row => row.model + '\0' + row.key_id).map(rows => {
        const aggregate = totals(rows);
        const hits = aggregate.input + aggregate.cached + aggregate.cache_write;
        const timed = rows.filter(row => row.ttft > 0);
        return {...aggregate, model: rows[0].model, key_id: rows[0].key_id,
          cache_pct: hits ? round(100 * aggregate.cached / hits, 2) : null,
          latency: round(sum(rows, 'latency') / rows.length), ttft: timed.length ? round(sum(timed, 'ttft') / timed.length) : null};
      }).sort((a, b) => b.requests - a.requests);
    }
    if (resource === 'keys') return keys;
    if (resource === 'events') return events.slice(0, 24);
    if (path === 'keys/sync') return {last_success: new Date(now).toISOString(), count: 2, error: ''};
    if (resource === 'models') return [...models.map(rate => ({id:rate.model,priced:true,reference:true,protocols:[rate.protocol]})),{id:'custom-unpriced-model',priced:false,reference:false,protocols:[]}];
    if (resource === 'prices') return prices;
    if (resource === 'sessions') {
      if (params.has('session') && params.has('key')) {
        return turns.filter(row => row.session === params.get('session') && row.key_id === params.get('key'));
      }
      return group(turns, row => row.session).map(rows => ({
        session: rows[0].session, key_id: rows[0].key_id, started: rows[0].started,
        turns: rows.length, cost: sum(rows, 'cost'), tokens: sum(rows, 'total')
      }));
    }
    if (resource.startsWith('turns/')) {
      const match = byTurn.get(decodeURIComponent(resource.slice('turns/'.length)));
      if (match) return match;
    }
    throw new Error('演示数据里没有这条记录');
  };
}
