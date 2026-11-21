const names={overview:'用量总览',keys:'Key 额度',events:'消费流水',sessions:'会话审计',prices:'模型价格',credentials:'凭证池'};
const content=document.querySelector('#content');
const message=document.querySelector('#message');
const usd=n=>n==null?'待计价':'$'+(n/1e9).toLocaleString('en-US',{maximumFractionDigits:9});
const integer=n=>n==null?'—':Number(n).toLocaleString('zh-CN');
const date=n=>n?new Date(n).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}):'—';
const flag=n=>n?'是':'否';
const keyNames=new Map();
let active='overview', epoch=0;
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
 const r=await fetch('/api/'+path,{
  ...options,
  headers:options.body?{'Content-Type':'application/json','X-CPA-Ext-Admin':'1'}:undefined
 });
 const body=await r.json().catch(()=>null);
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
  const tr=element('tr'),td=element('td','暂无记录','muted');
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
 const f=element('form'), inputs={};
 for(const [name,label,type,value] of fields){
  const l=element('label',label),i=element('input');
  i.type=type;
  if(type==='checkbox')i.checked=Boolean(value);
  else i.value=value??'';
  if(type==='number'){i.step='any';i.min='0';i.required=true;}
  if(type==='password')i.autocomplete='new-password';
  l.append(i);f.append(l);inputs[name]=i;
 }
 const submit=element('button','保存','primary');
 f.append(submit);
 f.addEventListener('submit',async e=>{
  e.preventDefault();submit.disabled=true;
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
 const data=await api('turns/'+encodeURIComponent(id));
 const t=data.turn, body=data.body;
 const p=element('div',null,'panel');
 p.append(action('返回',()=>load(active)),element('h2',t.model+' · '+date(t.started)));
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
 const query=new URLSearchParams({key:row.key_id,session:row.session});
 const rows=await api('sessions?'+query);
 content.replaceChildren(action('返回会话列表',()=>load('sessions')),element('h2',row.session),
 table(rows,[['请求时间',r=>date(r.started)],['模型','model'],['状态','status'],['费用',r=>usd(r.cost)],['Token',r=>integer(r.total)],['正文','audit_state'],['详情',r=>action('查看',()=>showTurn(r.id))]]));
}
function keyForm(row={}){
 return form([
  ['name','显示名称','text',row.name],['secret','CPA 客户端 Key','password',''],
  ['daily','日额度 USD','number',(row.daily||0)/1e9],['total','总额度 USD','number',(row.total||0)/1e9],
  ['enabled','已启用','checkbox',row.id?row.enabled:true]
 ],data=>api('keys',{method:'PUT',body:JSON.stringify({...data,id:row.id||'',daily:Math.round(data.daily*1e9),total:Math.round(data.total*1e9)})}));
}
const views={
 overview:rows=>{
  const sum=name=>rows.reduce((n,row)=>n+(row[name]||0),0);
  const all=sum('requests');
  const input=sum('input')+sum('cached')+sum('cache_write');
  return [cards([['请求数',integer(all)],['总 Token',integer(sum('total'))],['已计价费用',usd(sum('cost'))],['待计价事件',integer(sum('pending'))],['缓存命中率',input?(100*sum('cached')/input).toFixed(1)+'%':'—']]),
   table(rows,[['模型','model'],['API Key',r=>keyName(r.key_id)],['请求占比',r=>all?(100*r.requests/all).toFixed(1)+'%':'—'],['Token',r=>integer(r.total)],['费用',r=>usd(r.cost)],['缓存 %','cache_pct'],['平均延迟 ms','latency'],['平均首 Token ms','ttft']])];
 },
 keys:rows=>{
  const editor=panel('添加 Key',keyForm());
  return [editor,table(rows,[['名称',r=>r.name||keyName(r.id)],['启用',r=>flag(r.enabled)],['日上限',r=>r.daily?usd(r.daily):'无限'],['总上限',r=>r.total?usd(r.total):'无限'],['今日消费',r=>usd(r.spent_today)],['累计消费',r=>usd(r.spent)],['在途预留',r=>usd(r.in_flight)],['未核销',r=>usd(r.reserved_unresolved)],['可立即预留',r=>usd(r.admission_capacity)],['操作',r=>action('编辑',()=>editor.replaceChildren(element('h2',r.name||keyName(r.id)),keyForm(r))) ]])];
 },
 events:rows=>[table(rows,[['时间',r=>date(r.happened)],['API Key',r=>keyName(r.key_id)],['模型','model'],['输入',r=>integer(r.input)],['输出',r=>integer(r.output)],['缓存',r=>integer(r.cached)],['费用',r=>usd(r.cost)],['状态',r=>r.blocked||(r.cost==null?'待对账':'已结算')],['Turn',r=>r.turn_id?action('查看',()=>showTurn(r.turn_id)):'—']])],
 sessions:rows=>[table(rows,[['最后更新',r=>date(r.started)],['API Key',r=>keyName(r.key_id)],['会话','session'],['轮次','turns'],['费用',r=>usd(r.cost)],['操作',r=>action('打开',()=>showSession(r))]])],
 prices:rows=>[panel('新增价格版本',form([
  ['protocol','协议','text','chat'],['model','模型别名','text',''],['billed_model','CPA 账单模型','text',''],
  ['input','输入 USD / 1M','number',0],['output','输出 USD / 1M','number',0],
  ['cached','缓存读 USD / 1M','number',0],['cache_write','缓存写 USD / 1M','number',0],
  ['max_input','最大输入 Token','number',0],['max_output','最大输出 Token','number',0],['verified','费用上界已验证','checkbox',false]
 ],data=>{
  for(const name of ['input','output','cached','cache_write'])data[name]=Math.round(data[name]*1000);
  return api('prices',{method:'POST',body:JSON.stringify(data)});
 })),table(rows,[['版本','id'],['协议','protocol'],['模型','model'],['实际计费模型','billed_model'],['最大输入','max_input'],['最大输出','max_output'],['允许严格额度',r=>flag(r.verified)]])],
 credentials:rows=>[table(rows,[['名称','name'],['供应商','provider'],['状态','status'],['停用',r=>flag(r.disabled)],['不可用',r=>flag(r.unavailable)]])]
};
async function load(page){
 const token=++epoch;active=page;notice();
 document.querySelector('#title').textContent=names[page];
 document.querySelectorAll('nav button').forEach(b=>b.setAttribute('aria-current',String(b.dataset.page===page)));
 content.replaceChildren(element('p','正在加载…','muted'));
 try{
  const [rows,keys]=await Promise.all([api(page),page==='keys'?null:api('keys')]);
  for(const key of keys||rows.filter(row=>page==='keys'))if(key.name)keyNames.set(key.id,key.name);
  if(token===epoch)content.replaceChildren(...views[page](rows));
 }catch(error){
  if(token===epoch){content.replaceChildren();notice(error.message,true);}
 }
}
async function monitor(){
 const badge=document.querySelector('#health');
 try{
  const health=await api('health');
  badge.textContent=health.error?'采集异常 · '+health.error:'CPA 用量采集 · 正常';
 }catch{badge.textContent='采集状态不可用';}
}
document.querySelectorAll('nav button').forEach(b=>b.addEventListener('click',()=>{location.hash=b.dataset.page;}));
document.querySelector('#refresh').addEventListener('click',()=>{load(active);monitor();});
function locationPage(){const p=location.hash.slice(1);return Object.hasOwn(names,p)?p:'overview';}
window.addEventListener('hashchange',()=>load(locationPage()));
load(locationPage());monitor();setInterval(monitor,5000);
