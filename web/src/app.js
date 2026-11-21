const names={overview:'用量总览',keys:'Key 额度',events:'消费流水',sessions:'会话审计',prices:'模型价格'};
const content=document.querySelector('#content');
const loginForm=document.querySelector('#login-form');
const root=document.documentElement;
const descriptions={
 overview:'CPA 请求、Token、费用与缓存命中。时间以北京时间统计。',
 keys:'自动同步 CPA 客户端 Key，仅附加额度策略。创建与删除交给 CPA。',
 events:'查看 CPA 上报的用量、结算状态与关联会话。',
 sessions:'按会话检查请求正文、模型回复与工具调用。',
 prices:'内置参考价格用于估算，可选手动覆盖；高级上界单独配置。'
};
const message=document.querySelector('#message');
const usd=n=>n==null?'待计价':'$'+(n/1e9).toLocaleString('en-US',{maximumFractionDigits:9});
const usdSummary=n=>n>=1e9?'$'+(n/1e9).toLocaleString('en-US',{minimumFractionDigits:2,maximumFractionDigits:2}):usd(n);
const integer=n=>n==null?'—':Number(n).toLocaleString('zh-CN');
const date=n=>n?new Date(n).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}):'—';
const flag=n=>n?'是':'否';
const keyNames=new Map();
let active='overview', epoch=0, chartObserver;
let repaintCharts=[];
let demoData;
const element=(tag,value,cls)=>{
 const n=document.createElement(tag);
 if(value!=null)n.textContent=value;
 if(cls)n.className=cls;
 return n;
};
const action=(label,callback,cls)=>{
 const b=element('button',label,cls);
 b.type='button';
 b.addEventListener('click',()=>Promise.resolve().then(callback).catch(error=>notice(error.message,true)));
 return b;
};
const keyName=id=>keyNames.get(id)||id?.slice(0,10)||'未知';
function notice(text='',error=false){message.textContent=text;message.className=error?'error strong':'';}
async function api(path, options={}){
 if(demoData && !['health','session','logout'].includes(path)){
  if(options.method && options.method!=='GET')throw Error('演示模式只读');
  return demoData(path);
 }
 const r=await fetch('api/'+path,{
  ...options,
  headers:options.body||(options.method&&options.method!=='GET')?{'Content-Type':'application/json','X-CPA-Ext-Admin':'1'}:undefined
 });
 const body=await r.json().catch(()=>null);
 if(r.status===401 && !loginForm){location.reload();throw Error('登录已失效');}
 if(!r.ok)throw Error(body?.error||'请求失败 ('+r.status+')');
 return body;
}
function table(rows,cols){
 const wrap=element('div',null,'panel table');
 const t=element('table'), header=element('thead'), tr=element('tr');
 cols.forEach(([label])=>tr.append(element('th',label)));
 header.append(tr);t.append(header);
 const body=element('tbody');
 for(const row of rows){
  const tr=element('tr');
  for(const [,get] of cols){
   const val=typeof get==='function'?get(row):row[get];
   const td=element('td');
   if(val instanceof Node)td.append(val);
   else td.textContent=val??'—';
   tr.append(td);
  }
  body.append(tr);
 }
 if(!rows.length){
  const tr=element('tr'),td=element('td','暂无记录','empty-cell');
  td.colSpan=cols.length;tr.append(td);body.append(tr);
 }
 t.append(body);wrap.append(t);return wrap;
}
function cards(items){
 const grid=element('div',null,'cards');
 for(const [label,val] of items){
  const card=element('div',null,'card');
  card.append(element('small',label),element('strong',val));
  grid.append(card);
 }
 return grid;
}
function form(fields,save){
 const f=element('form',null,'editor'), inputs={};
 for(const [name,label,type,value] of fields){
  const l=element('label',label),i=element('input');
  i.type=type;
  if(type==='checkbox')i.checked=Boolean(value);
  else i.value=value??'';
  if(type==='number'){i.step='any';i.min='0';i.required=true;}
  if(type==='password')i.autocomplete='new-password';
  i.disabled=Boolean(demoData);
  l.append(i);f.append(l);inputs[name]=i;
 }
 const submit=element('button','保存','primary');
 submit.disabled=Boolean(demoData);
 f.append(submit);
 f.addEventListener('submit',async e=>{
  e.preventDefault();
  if(demoData)return;
  submit.disabled=true;
  const data=Object.fromEntries(Object.entries(inputs).map(([k,i])=>[k,i.type==='checkbox'?i.checked:i.type==='number'?i.valueAsNumber:i.value.trim()]));
  try{await save(data);await load(active);notice('已保存。');}
  catch(error){notice(error.message,true);}
  finally{submit.disabled=false;}
 });
 return f;
}
function panel(title,child){
 const p=element('div',null,'panel');p.append(element('h2',title),child);return p;
}
function detail(title,text){
 const d=element('details');d.append(element('summary',title),element('pre',text));return d;
}
function decode(raw){
 if(!raw)return '';
 const value=new TextDecoder().decode(Uint8Array.from(atob(raw),c=>c.charCodeAt(0)));
 try{return JSON.stringify(JSON.parse(value),null,2);}catch{return value;}
}
async function showTurn(id){
 const token=++epoch;
 const data=await api('turns/'+encodeURIComponent(id));
 if(token!==epoch)return;
 const t=data.turn, body=data.body;
 const p=element('div',null,'panel');
 const heading=element('div',null,'crumbs');
 heading.append(action('返回',()=>load(active)),element('h2',t.model+' · '+date(t.started)));
 p.append(heading);
 p.append(cards([
  ['消费',usd(t.cost)],['输入',integer(t.input)],['输出',integer(t.output)],
  ['缓存读取',integer(t.cached)],['缓存写入',integer(t.cache_write)]
 ]));
 if(t.blocked)p.append(element('p',t.blocked,'error'));
 if(!body)p.append(element('p','正文不可用 · '+t.audit_state,'muted'));
 else{
  p.append(element('p','采集状态 · '+t.audit_state,'muted'));
  p.append(detail('Prompt / 工具结果',decode(body.request_raw)));
  const response=detail('Response',body.output?.text||'');
  response.open=true;p.append(response);
  if(body.output?.tools?.length)p.append(detail('Tool Calls',JSON.stringify(body.output.tools,null,2)));
  p.append(detail('响应原文',decode(body.response_raw)));
 }
 content.replaceChildren(p);
}
async function showSession(row){
 const token=++epoch;
 const query=new URLSearchParams({key:row.key_id,session:row.session});
 const rows=await api('sessions?'+query);
 if(token!==epoch)return;
 const heading=element('div',null,'crumbs');
 heading.append(action('返回会话',()=>load('sessions')),element('h2',keyName(row.key_id)+' · '+row.session));
 content.replaceChildren(heading,
 table(rows,[['请求时间',r=>date(r.started)],['模型','model'],['状态','status'],['费用',r=>usd(r.cost)],['Token',r=>integer(r.total)],['正文','audit_state'],['详情',r=>action('查看',()=>showTurn(r.id))]]));
}
function keyForm(row){
 const f=form([
  ['name','显示名称','text',row.name],
  ['daily','日额度 USD（0 为无限）','number',(row.daily||0)/1e9],
  ['total','总额度 USD（0 为无限）','number',(row.total||0)/1e9],
  ['enabled','允许通过扩展网关（关闭将拒绝请求）','checkbox',row.enabled]
 ],data=>api('keys',{method:'PUT',body:JSON.stringify({...data,id:row.id,daily:Math.round(data.daily*1e9),total:Math.round(data.total*1e9)})}));
 if(row.cpa_present===false)f.querySelectorAll('input,button').forEach(n=>n.disabled=true);
 return f;
}
function cpaLink(label,route){
 const a=element('a',label,'button-link');
 const target=new URL('/management.html',location.href);target.hash=route;
 if(window.parent===window && target.port==='8481')target.port='8482';
 try{
  if(window.parent!==window && window.parent.location.origin===location.origin){
   target.pathname=window.parent.location.pathname;
   a.addEventListener('click',e=>{e.preventDefault();window.parent.location.hash=route;});
  }
 }catch{}
 a.href=target.href;return a;
}
function priceForm(row={}){
 const f=form([
  ['protocol','协议','text',row.protocol||'chat'],['model','模型 / 别名','text',row.model||''],
  ['billed_model','CPA 账单模型','text',row.billed_model||''],
  ['input','输入 USD / 1M','number',(row.input||0)/1000],['output','输出 USD / 1M','number',(row.output||0)/1000],
  ['cached','缓存读 USD / 1M','number',(row.cached||0)/1000],['cache_write','缓存写 USD / 1M','number',(row.cache_write||0)/1000],
  ['max_input','最大输入 Token','number',row.is_reference?0:row.max_input||0],
  ['max_output','最大输出 Token','number',row.is_reference?0:row.max_output||0],
  ['verified','费用上界已验证','checkbox',row.is_reference?false:row.verified]
 ],data=>{
  for(const name of ['input','output','cached','cache_write'])data[name]=Math.round(data[name]*1000);
  return api('prices',{method:'POST',body:JSON.stringify({...data,is_reference:false})});
 });
 const advanced=element('details',null,'advanced-settings');
 advanced.append(element('summary','高级：严格额度费用上界（可选）'),element('p','普通费用估算无需填写。只有能证明费用上界时才验证；参考价格不自动允许严格额度。','muted'));
 for(const label of [...f.querySelectorAll('label')].slice(-3))advanced.append(label);
 f.insertBefore(advanced,f.querySelector('button'));return f;
}
const rate=n=>n==null?'—':(n/1000).toLocaleString('en-US',{maximumFractionDigits:6});
function priceSource(row){
 const wrap=element('span',null,'source-info');
 wrap.append(element('span',row.source||(row.is_reference?'内置参考':'手动设置')));
 if(row.source_date)wrap.append(element('small',' · '+row.source_date));
 try{
  const url=new URL(row.source_url);
  if(['https:','http:'].includes(url.protocol)){
   const a=element('a','来源');a.href=url.href;a.target='_blank';a.rel='noopener noreferrer';wrap.append(document.createTextNode(' · '),a);
  }
 }catch{}
 return wrap;
}
function timeSeries(series,days){
 const box=element('section',null,'panel');
 const heading=element('div',null,'chart-heading');
 const select=element('select');select.setAttribute('aria-label','趋势指标');
 for(const [key,label] of [['requests','请求数'],['tokens','Token'],['cost','已计价费用']]){
  const option=element('option',label);option.value=key;select.append(option);
 }
 heading.append(element('h2','用量趋势'),select);box.append(heading);
 if(!series.length){
  select.disabled=true;box.append(element('div','产生首笔请求后，这里会显示用量趋势。','chart-empty'));return box;
 }
 const index=new Map(series.map(row=>[row.day,row]));
 const now=Date.now()+8*3600000;
 const points=Array.from({length:days},(_,i)=>{
  const day=new Date(now-(days-1-i)*86400000).toISOString().slice(0,10);
  return index.get(day)||{day,requests:0,tokens:0,cost:0};
 });
 const canvas=element('canvas',null,'chart-canvas');
 canvas.setAttribute('role','img');
 canvas.tabIndex=0;
 const readout=element('p',null,'chart-readout');
 box.append(canvas,readout);
 let selected=points.length-1;
 let graph;
 const draw=()=>{
  if(!canvas.isConnected)return;
  const w=canvas.clientWidth,h=canvas.clientHeight,dpr=window.devicePixelRatio||1;
  if(w===0||h===0)return;
  const ctx=canvas.getContext('2d');
  if(!ctx)return;
  canvas.width=w*dpr;canvas.height=h*dpr;ctx.scale(dpr,dpr);
  const css=getComputedStyle(root), color=k=>css.getPropertyValue(k).trim();
  const key=select.value, values=points.map(row=>row[key]||0), max=Math.max(1,...values);
  canvas.setAttribute('aria-label',select.selectedOptions[0].textContent+'趋势，合计 '+(key==='cost'?usd(values.reduce((a,b)=>a+b,0)):integer(values.reduce((a,b)=>a+b,0))));
  const axis=k=>key==='cost'?(k===0?'$0':'$'+(k/1e9).toPrecision(2)):new Intl.NumberFormat('zh-CN',{notation:'compact',maximumFractionDigits:1}).format(k);
  ctx.font='11px system-ui';
  const pad={top:10,right:12,bottom:25,left:Math.max(35,ctx.measureText(axis(max)).width+11)};
  const width=w-pad.left-pad.right,height=h-pad.top-pad.bottom;
  if(width<10)return;
  graph={left:pad.left,width};
  const x=i=>pad.left+i*width/(points.length-1);
  const y=v=>pad.top+(1-v/max)*height;
  ctx.textAlign='right';ctx.textBaseline='middle';ctx.fillStyle=color('--muted');
  for(let i=0;i<=4;i++){
   const v=max*i/4, py=y(v);
   if(max>=4||i===0||i===4)ctx.fillText(axis(v),pad.left-7,py);
   ctx.strokeStyle=color('--line');ctx.lineWidth=1;
   ctx.beginPath();ctx.moveTo(pad.left,py);ctx.lineTo(w-pad.right,py);ctx.stroke();
  }
  ctx.textBaseline='alphabetic';
  const stride=Math.ceil((points.length-1)/Math.max(2,Math.floor(width/72)));
  const labelAt=new Set([0,points.length-1]);
  for(let i=stride;i<points.length-stride/2;i+=stride)labelAt.add(i);
  for(const i of labelAt){ctx.textAlign=i===0?'left':i===points.length-1?'right':'center';ctx.fillText(points[i].day.slice(5),x(i),h-5);}
  ctx.beginPath();values.forEach((v,i)=>i?ctx.lineTo(x(i),y(v)):ctx.moveTo(x(i),y(v)));
  ctx.lineTo(x(values.length-1),y(0));ctx.lineTo(x(0),y(0));ctx.closePath();
  ctx.fillStyle=color('--accent-soft');ctx.fill();
  ctx.beginPath();values.forEach((v,i)=>i?ctx.lineTo(x(i),y(v)):ctx.moveTo(x(i),y(v)));
  ctx.strokeStyle=color('--accent');ctx.lineWidth=2;ctx.lineJoin='round';ctx.stroke();
  const selectedX=x(selected);
  ctx.strokeStyle=color('--accent-line');ctx.lineWidth=1;ctx.beginPath();
  ctx.moveTo(selectedX,pad.top);ctx.lineTo(selectedX,pad.top+height);ctx.stroke();
  ctx.fillStyle=color('--accent');ctx.beginPath();
  ctx.arc(selectedX,y(values[selected]),3.5,0,Math.PI*2);ctx.fill();
  readout.textContent=points[selected].day+' · '+(key==='cost'?usdSummary(values[selected]):integer(values[selected]));
 };
 select.addEventListener('change',draw);
 canvas.addEventListener('pointermove',event=>{
  if(!graph)return;
  const next=Math.round((event.offsetX-graph.left)/graph.width*(points.length-1));
  const clamped=Math.max(0,Math.min(points.length-1,next));
  if(clamped!==selected){selected=clamped;draw();}
 });
 canvas.addEventListener('keydown',event=>{
  if(event.key!=='ArrowLeft'&&event.key!=='ArrowRight')return;
  event.preventDefault();
  selected=Math.max(0,Math.min(points.length-1,selected+(event.key==='ArrowLeft'?-1:1)));
  draw();
 });
 chartObserver=new ResizeObserver(draw);chartObserver.observe(canvas);
 repaintCharts.push(draw);
 return box;
}

function sharePanel(rows){
 const panel=element('section',null,'panel');
 const header=element('div',null,'chart-heading');
 const group=element('select');group.setAttribute('aria-label','请求分布维度');
 for(const [value,label] of [['model','按模型'],['key_id','按 Key']]){
  const item=element('option',label);item.value=value;group.append(item);
 }
 header.append(element('h2','请求分布'),group);
 const list=element('ul',null,'shares');panel.append(header,list);
 if(!rows.length){
  group.disabled=true;list.append(element('li','暂无请求分布','muted'));return panel;
 }
 const render=()=>{
  const counts=new Map();
  for(const row of rows){const k=row[group.value];counts.set(k,(counts.get(k)||0)+row.requests);}
  const total=[...counts.values()].reduce((a,b)=>a+b,0);
  list.replaceChildren();
  for(const [id,count] of [...counts].sort((a,b)=>b[1]-a[1]).slice(0,6)){
   const name=group.value==='key_id'?keyName(id):id||'未知模型';
   const item=element('li'),label=element('div',null,'share-label'),bar=element('meter');
   const caption=element('span',name);caption.title=name;
   label.append(caption,element('strong',(100*count/total).toFixed(1)+'%'));
   bar.min=0;bar.max=total;bar.value=count;bar.setAttribute('aria-label',name+' '+count+' 次');
   item.append(label,bar);list.append(item);
  }
 };
 group.addEventListener('change',render);render();return panel;
}

function state(text,kind){
 const marker=element('span',null,'state');marker.dataset.status=kind;
 marker.append(element('span'),document.createTextNode(text));return marker;
}

function usageCards(rows,series,days){
 const count=(items,key)=>items.reduce((n,row)=>n+(row[key]||0),0);
 const requests=count(series,'requests');
 const tokens=count(series,'tokens');
 const spend=count(series,'cost');
 const pending=count(series,'pending');
 const fails=count(series,'failures');
 const cached=count(series,'cached');
 const input=count(series,'input_tokens')||count(rows,'input')+count(rows,'cached')+count(rows,'cache_write');
 const rate=n=>(100*n).toFixed(1)+'%';
 const metrics=[
  ['请求数',integer(requests),'requests','live',requests?integer(requests-fails)+' 次成功':'等待首笔请求'],
  ['总 Token',integer(tokens),'tokens','neutral',cached?'缓存命中 '+integer(cached)+' Token':'输入、输出与缓存'],
  ['已计价费用',usdSummary(spend),'cost','neutral',pending?pending+' 笔待计价':'按模型价格估算'],
  ['缓存命中率',input?rate((cached||count(rows,'cached'))/input):'—','cacheRate','neutral',input?integer(input)+' 输入 Token':'等待用量统计'],
  ['请求成功率',requests?rate((requests-fails)/requests):'—','successRate','live',integer(fails)+' 次失败'],
 ];
 const grid=element('div',null,'cards usage-cards');
 const index=new Map(series.map(row=>[row.day,row]));
 const now=Date.now()+8*3600000;
 const points=Array.from({length:days},(_,i)=>index.get(new Date(now-(days-1-i)*86400000).toISOString().slice(0,10)));
 const value=(point,key)=>{
  if(!point)return 0;
  if(key==='cacheRate')return point.input_tokens?point.cached/point.input_tokens:0;
  if(key==='successRate')return point.requests?(point.requests-(point.failures||0))/point.requests:0;
  return point[key]||0;
 };
 for(const [label,number,key,tone,note] of metrics){
  const card=element('div',null,'card kpi kpi-'+tone);
  card.append(element('small',label),element('strong',number),element('p',note,'kpi-note'));
  if(series.length){
   const spark=element('canvas',null,'sparkline');
   spark.width=320;spark.height=43;spark.setAttribute('aria-hidden','true');
   card.append(spark);
   const draw=()=>{
    if(!spark.isConnected)return;
    const ctx=spark.getContext('2d'),color=getComputedStyle(card).getPropertyValue('--tone').trim();
    const values=points.map(point=>value(point,key));
    const max=Math.max(...values)||1;
    ctx.clearRect(0,0,spark.width,spark.height);
    ctx.beginPath();
    values.forEach((v,i)=>{const x=i*319/(values.length-1),y=39-(v/max)*32;i?ctx.lineTo(x,y):ctx.moveTo(x,y);});
    ctx.strokeStyle=color;ctx.lineWidth=2;ctx.lineJoin='round';ctx.stroke();
   };
   repaintCharts.push(draw);requestAnimationFrame(draw);
  }
  grid.append(card);
 }
 return grid;
}

const views={
 overview:(rows,series,days)=>{
  const sum=name=>rows.reduce((n,row)=>n+(row[name]||0),0);
  const all=sum('requests');
  const charts=element('div',null,'chart-grid');
  charts.append(timeSeries(series,days),sharePanel(rows));
  return [usageCards(rows,series,days),charts,element('h2','模型与 Key 明细','section-heading'),
   table(rows,[['模型','model'],['API Key',r=>keyName(r.key_id)],['请求占比',r=>all?(100*r.requests/all).toFixed(1)+'%':'—'],['Token',r=>integer(r.total)],['费用',r=>usd(r.cost)],['缓存 %','cache_pct'],['平均延迟 ms','latency'],['平均首 Token ms','ttft']])];
 },
 keys:(rows,unused,days,sync)=>{
  const controls=element('div',null,'key-tools');
  const synchronize=action('从 CPA 同步',async()=>{
   synchronize.disabled=true;
   try{await api('keys/sync',{method:'POST',body:'{}'});await load('keys');notice('CPA Key 已同步。');}
   finally{synchronize.disabled=Boolean(demoData);}
  });synchronize.disabled=Boolean(demoData);
  controls.append(synchronize,cpaLink('在 CPA 管理 Key','/config'));
  const status=sync?.error?'同步失败：'+sync.error:sync?.last_success&&!String(sync.last_success).startsWith('0001')?'最近同步：'+new Date(sync.last_success).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'})+' · '+(sync.count??0)+' 个 Key':'尚未同步，请从 CPA 同步 Key。';
  controls.append(element('p',demoData?'演示模式：同步与保存均禁用。':status,'muted'));
  const editor=panel('额度策略',element('p','选择下方 Key 编辑额度。新增 / 删除请使用 CPA；已移除 Key 仅保留历史记录。','muted'));
  if(demoData&&rows[0])editor.append(keyForm(rows[0]));
  return [panel('CPA 客户端 Key',controls),editor,table(rows,[['名称',r=>r.name||keyName(r.id)],['Key','masked_key'],['CPA 状态',r=>state(r.cpa_present===false?'已移除':'已同步',r.cpa_present===false?'pending':'ok')],['额度策略',r=>state(r.enabled?'已启用':'已停用',r.enabled?'ok':'error')],['日上限',r=>r.daily?usd(r.daily):'无限'],['总上限',r=>r.total?usd(r.total):'无限'],['今日消费',r=>usd(r.spent_today)],['累计消费',r=>usd(r.spent)],['在途预留',r=>usd(r.in_flight)],['未核销',r=>usd(r.reserved_unresolved)],['可立即预留',r=>usd(r.admission_capacity)],['操作',r=>{
   const b=action(demoData?'查看':'编辑',()=>{editor.replaceChildren(element('h2',r.name||keyName(r.id)),element('p',r.masked_key||'Key 已脱敏','muted'),keyForm(r));editor.scrollIntoView({block:'nearest',behavior:'smooth'});});b.disabled=r.cpa_present===false;return b;
  }]])];
 },
 events:rows=>[table(rows,[['时间',r=>date(r.happened)],['API Key',r=>keyName(r.key_id)],['模型','model'],['输入',r=>integer(r.input)],['输出',r=>integer(r.output)],['缓存',r=>integer(r.cached)],['费用',r=>usd(r.cost)],['状态',r=>state(r.blocked||(r.cost==null?'待对账':'已结算'),r.blocked?'error':r.cost==null?'pending':'ok')],['Turn',r=>r.turn_id?action('查看',()=>showTurn(r.turn_id)):'—']])],
 sessions:rows=>[table(rows,[['最后更新',r=>date(r.started)],['API Key',r=>keyName(r.key_id)],['会话','session'],['轮次','turns'],['费用',r=>usd(r.cost)],['操作',r=>action('打开',()=>showSession(r),'row-action')]])],
 prices:(rows,unused,days,extra)=>{
  const note=element('p','参考价格仅用于估算，非上游实扣；适用标准文本，不适用长上下文、特殊服务层级或缓存写入 TTL。未定价模型不能按零费用处理。','muted');
  const editor=panel('可选：手动覆盖价格',element('p','通常无需手动填写。选择参考价格“覆盖”，或展开新增价格。历史版本继续保留。','muted'));
  const create=element('details');create.append(element('summary','新增自定义价格'),priceForm());editor.append(create);
  const coverage=panel('CPA 可用模型与价格匹配',extra?.models_error?element('p','模型列表暂不可用：'+extra.models_error,'muted'):table(extra?.models||[],[['模型','id'],['计价状态',r=>state(r.priced?(r.reference?'参考价格':'手动价格'):'未定价',r.priced?'ok':'pending')],['匹配协议',r=>(r.protocols||[]).join(' / ')||'—']]));
  return [note,coverage,editor,element('h2','参考价格与历史版本','section-heading'),table(rows,[['版本','id'],['状态',r=>(r.active===false||r.active===0)?'历史版本':'当前版本'],['协议','protocol'],['模型','model'],['实际计费模型','billed_model'],['输入 $/1M',r=>rate(r.input)],['输出 $/1M',r=>rate(r.output)],['缓存读 $/1M',r=>rate(r.cached)],['缓存写 $/1M',r=>rate(r.cache_write)],['类型',r=>r.is_reference?'内置参考':'手动覆盖'],['来源 / 日期',priceSource],['严格额度',r=>r.verified?'上界已验证':'未验证'],['操作',r=>action(demoData?'查看':'覆盖',()=>{editor.replaceChildren(element('h2',demoData?'价格预览':'手动覆盖 · '+r.model),priceForm(r));editor.scrollIntoView({block:'nearest',behavior:'smooth'});})]])];
 }

};
async function load(page){
 const token=++epoch;active=page;notice();chartObserver?.disconnect();repaintCharts=[];
 const days=Number(document.querySelector('#days').value);
 document.querySelector('#days').closest('label').hidden=page!=='overview';
 document.title=names[page]+' · cpa-ext';
 document.querySelector('#description').textContent=descriptions[page];
 document.querySelectorAll('nav button').forEach(b=>b.setAttribute('aria-current',String(b.dataset.page===page)));
 content.replaceChildren(element('p','正在加载…','muted'));
 try{
  const [rows,keys,series,extra]=await Promise.all([
   api(page==='overview'?'overview?days='+days:page),
   page==='keys'?null:api('keys'),
   page==='overview'?api('series?days='+days):[],
    page==='keys'?api('keys/sync').catch(error=>({error:error.message})):page==='prices'?api('models').then(models=>({models})).catch(error=>({models_error:error.message})):null
  ]);
  for(const key of keys||(page==='keys'?rows:[]))if(key.name)keyNames.set(key.id,key.name);
  if(token===epoch)content.replaceChildren(...views[page](rows,series,days,extra));
 }catch(error){
  if(token===epoch){content.replaceChildren();notice(error.message,true);}
 }
}
async function monitor(){
 const badge=document.querySelector('#health');
 try{
  const health=await api('health');
  const ok=!health.error&&!String(health.last_poll).startsWith('0001');
  badge.dataset.status=ok?'ok':'error';
  badge.title=health.error||'';
  badge.replaceChildren(element('span'),document.createTextNode(ok?'采集正常':health.error?'采集异常':'等待采集'));
 }catch{
  badge.dataset.status='error';badge.replaceChildren(element('span'),document.createTextNode('采集状态不可用'));
 }
}
const demoStorageKey='cpa-ext-data-view';

async function changeDemo(enabled, remember){
 if(enabled){
  const {createDemo}=await import('./demo.js');
  demoData=createDemo();
 }else{
  demoData=undefined;
 }
 if(remember){
  try{sessionStorage.setItem(demoStorageKey,enabled?'demo':'live');}catch{}
  const url=new URL(location.href);
  if(url.searchParams.has('demo')){
   url.searchParams.delete('demo');
   history.replaceState(history.state,'',url);
  }
 }
 keyNames.clear();
 document.querySelector('#demo-note').hidden=!enabled;
 document.querySelector('#demo-toggle').textContent=enabled?'查看真实数据':'显示演示数据';
}

async function chooseInitialData(){
 const flag=new URL(location.href).searchParams.get('demo');
 if(flag==='1'||flag==='0'){
  await changeDemo(flag==='1',true);
  return;
 }
 let saved;
 try{saved=sessionStorage.getItem(demoStorageKey);}catch{}
 if(saved){
  await changeDemo(saved==='demo',false);
  return;
 }
 const rows=await Promise.all([api('overview?days=30'),api('keys'),api('prices')]);
 if(rows.every(items=>items.length===0))await changeDemo(true,false);
}

async function startDashboard(){
 const toggle=document.querySelector('#demo-toggle');
 toggle.addEventListener('click',async ()=>{
  toggle.disabled=true;
  try{
   await changeDemo(!demoData,true);
   await load(locationPage());
  }catch(error){notice(error.message,true);}
  finally{toggle.disabled=false;}
 });
 content.replaceChildren(element('p','正在加载…','muted'));
 try{await chooseInitialData();}catch{}
 toggle.disabled=false;
 load(locationPage());
 monitor();
 setInterval(monitor,5000);
}

function locationPage(){const p=location.hash.slice(1);return Object.hasOwn(names,p)?p:'overview';}
const preferred=matchMedia('(prefers-color-scheme: dark)');
const panelThemeTokens={
  '--host-page':'--bg-secondary',
  '--host-card':'--bg-primary',
  '--host-soft':'--bg-tertiary',
  '--host-hover':'--bg-hover',
  '--host-ink':'--text-primary',
  '--host-muted':'--text-secondary',
  '--host-subtle':'--text-tertiary',
  '--host-line':'--border-color',
  '--host-border':'--border-primary',
  '--host-accent':'--primary-color',
  '--host-button':'--primary-active',
  '--host-contrast':'--primary-contrast',
  '--host-success':'--viz-success',
  '--host-danger':'--warning-text',
  '--host-danger-bg':'--warning-bg',
  '--host-shadow':'--shadow',
  '--host-radius':'--radius-md'
};

let panelDocument;
if(window.parent!==window){
  try {
    if(window.parent.location.origin===location.origin){
      panelDocument=window.parent.document;
      root.dataset.embedded='';
    }
  }catch{}
}

function syncTheme(){
  let theme=preferred.matches?'dark':'white';
  if(panelDocument){
    const panelRoot=panelDocument.documentElement;
    const style=window.parent.getComputedStyle(panelRoot);
    const activeTheme=panelRoot.dataset.theme || (style.getPropertyValue('--bg-secondary').trim() ? 'light' : '');
    if(['white','light','dark'].includes(activeTheme))theme=activeTheme;
    for(const [local,upstream] of Object.entries(panelThemeTokens)){
      const value=style.getPropertyValue(upstream).trim();
      if(value)root.style.setProperty(local,value);
      else root.style.removeProperty(local);
    }
    if(panelDocument.body){
      root.style.setProperty('--host-font',window.parent.getComputedStyle(panelDocument.body).fontFamily);
    }
  }
  root.dataset.theme=theme;
  requestAnimationFrame(()=>repaintCharts.forEach(draw=>draw()));
}

syncTheme();
preferred.addEventListener('change',syncTheme);
if(panelDocument){
  new MutationObserver(syncTheme).observe(panelDocument.documentElement,{
    attributes:true,attributeFilter:['data-theme']
  });
}

if(loginForm){
 loginForm.addEventListener('submit',async event=>{
  event.preventDefault();
  const errorText=document.querySelector('#login-error');
  const btn=loginForm.querySelector('button');
  errorText.textContent='';btn.disabled=true;
  try{
   await api('session',{method:'POST',body:JSON.stringify({password:loginForm.elements.password.value})});
   location.reload();
  }catch(error){errorText.textContent=error.message;}
  finally{btn.disabled=false;}
 });
}else{
 document.querySelectorAll('nav button').forEach(b=>b.addEventListener('click',()=>{location.hash=b.dataset.page;}));
 document.querySelector('#refresh').addEventListener('click',()=>{load(active);monitor();});
 document.querySelector('#days').addEventListener('change',()=>load('overview'));
 document.querySelector('#cpa-auth-link')?.replaceWith(cpaLink('CPA 认证文件 ↗','/auth-files'));
 document.querySelector('#logout').addEventListener('click',async ()=>{
  try{await api('logout',{method:'POST',body:'{}'});location.reload();}
  catch(error){notice(error.message,true);}
 });
 window.addEventListener('hashchange',()=>load(locationPage()));
 startDashboard();
}
