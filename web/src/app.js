const names={overview:'用量总览',keys:'密钥限制',events:'请求明细',sessions:'会话审计'};
const content=document.querySelector('#content');
const loginForm=document.querySelector('#login-form');
const root=document.documentElement;
const descriptions={
 overview:'CPA 请求、Token、费用与缓存命中。时间以北京时间统计。',
 keys:'按密钥设置周限额、总限额和可调用模型，并查看消费。',
 events:'逐条查看 CPA 上报的请求：用量、费用、结果与关联会话。',
 sessions:'按会话检查请求正文、模型回复与工具调用。列出所选时间内有请求的会话。'
};
const message=document.querySelector('#message');
const usd=n=>n==null?'待计价':'$'+(n/1e9).toLocaleString('en-US',{maximumFractionDigits:9});
const usdSummary=n=>n>=1e9?'$'+(n/1e9).toLocaleString('en-US',{minimumFractionDigits:2,maximumFractionDigits:2}):usd(n);
const integer=n=>n==null?'—':Number(n).toLocaleString('zh-CN');
const date=n=>n?new Date(n).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'}):'—';
const compactTokens=new Intl.NumberFormat('en-US',{maximumFractionDigits:2});
function tokenCount(n){
 if(n>=1e9)return compactTokens.format(n/1e9)+'B';
 if(n>=1e6)return compactTokens.format(n/1e6)+'M';
 return integer(n);
}
const flag=n=>n?'是':'否';
const keyNames=new Map();
let active='overview', epoch=0, chartObserver;
let repaintCharts=[];
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
 for(const [name,label,type,value,options] of fields){
  const l=element('label',label);
  let i;
  if(type==='select'){
   i=element('select');
   for(const [v,text] of options)i.append(new Option(text,v));
   i.value=value??'';
  }else{
   i=element('input');
   i.type=type;
   if(type==='checkbox')i.checked=Boolean(value);
   else i.value=value??'';
   if(type==='number'){i.step='any';i.min='0';i.required=true;}
   if(type==='password')i.autocomplete='new-password';
  }
  l.append(i);f.append(l);inputs[name]=i;
 }
 const submit=element('button','保存','primary');
 f.append(submit);
 f.addEventListener('submit',async e=>{
  e.preventDefault();
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
function rawDetail(title,raw){
 const section=detail(title,'');
 const render=()=>{
  if(!section.open)return;
  try{section.querySelector('pre').textContent=decode(raw);}
  catch{section.querySelector('pre').textContent='原文解码失败。';}
  section.removeEventListener('toggle',render);
 };
 section.addEventListener('toggle',render);
 return section;
}
async function showTurn(id,back=()=>load(active)){
 const token=++epoch;
 const data=await api('turns/'+encodeURIComponent(id));
 if(token!==epoch)return;
 const t=data.turn, body=data.body;
 const p=element('div',null,'panel');
 const heading=element('div',null,'crumbs');
 heading.append(action('返回',back),element('h2',t.model+' · '+date(t.started)));
 p.append(heading);
 p.append(cards([
  ['消费',usd(t.cost)],['输入',tokenCount(t.input)],['输出',tokenCount(t.output)],
  ['缓存读取',tokenCount(t.cached)],['缓存写入',tokenCount(t.cache_write)]
 ]));
 if(t.blocked)p.append(element('p',t.blocked,'error'));
 if(data.body_status==='too_large'){
  p.append(element('p','归档超过 32 MiB，已保留原文。','muted'));
  const download=element('a','下载完整归档（JSON.gz）');
  download.href='api/turns/'+encodeURIComponent(id)+'/archive';
  p.append(download);
 }
 else if(!body)p.append(element('p','正文不可用 · '+t.audit_state,'muted'));
 else{
  p.append(element('p','采集状态 · '+t.audit_state,'muted'));
  p.append(rawDetail('Prompt / 工具结果',body.request_raw));
  const response=detail('Response',body.output?.text||'');
  response.open=true;p.append(response);
  if(body.output?.tools?.length)p.append(detail('Tool Calls',JSON.stringify(body.output.tools,null,2)));
  p.append(rawDetail('响应原文',body.response_raw));
 }
 content.replaceChildren(p);
}
async function showSession(row){
 const token=++epoch;
 const q=new URLSearchParams({key:row.key_id,session:row.session,page:query.page.turns,size:query.size.turns});
 const data=await api('sessions?'+q);
 if(token!==epoch)return;
 const heading=element('div',null,'crumbs');
 heading.append(action('返回会话',()=>load('sessions')),element('h2',keyName(row.key_id)+' · '+row.session));
 content.replaceChildren(heading,
  table(data.rows,[['请求时间',r=>date(r.started)],['模型','model'],['状态','status'],['费用',r=>usd(r.cost)],['Token',r=>tokenCount(r.total)],['正文','audit_state'],['详情',r=>action('查看',()=>showTurn(r.id,()=>showSession(row)))]]),
  pager('turns',data.total,()=>showSession(row)));
}
// CPAMC keeps client key names only in this browser: localStorage on the shared
// origin, one entry per API base, XOR-masked with the host. Only CPAMC's name
// identifiers are stored there, so the server matches them to the keys.
function cpamcNames(){
 const found=[],prefix='api-key-names:v1:',seed='cli-proxy-api-webui::secure-storage|'+location.host;
 try{
  for(let n=0;n<localStorage.length;n++){
   const slot=localStorage.key(n);
   if(!slot.startsWith(prefix))continue;
   try{
    let text=localStorage.getItem(slot);
    const masked=/^enc::v([12])::/.exec(text);
    if(masked){
     const pad=new TextEncoder().encode(masked[1]==='1'?seed+'|'+navigator.userAgent:seed);
     const bytes=Uint8Array.from(atob(text.slice(masked[0].length)),c=>c.charCodeAt(0));
     text=new TextDecoder().decode(bytes.map((b,i)=>b^pad[i%pad.length]));
    }
    const names=Object.fromEntries(Object.entries(JSON.parse(text)).filter(([id,name])=>/^[a-f0-9]{64}$/.test(id)&&typeof name==='string'&&name.trim()));
    if(Object.keys(names).length)found.push({base:slot.slice(prefix.length),names});
   }catch{}
  }
 }catch{}
 return found.sort((a,b)=>a.base.startsWith(location.origin)-b.base.startsWith(location.origin));
}
async function syncNames(){
 for(const {base,names} of cpamcNames()){
  try{await api('keys/names',{method:'POST',body:JSON.stringify({base,names})});}catch{}
 }
}
const modelList=text=>text.split(/[,，\s]+/).filter(Boolean);
function allowedModelsCell(models){
 if(!models?.length)return '全部';
 if(models.length===1)return element('span',models[0],'allowed-models');
 const details=element('details',null,'allowed-models');
 const list=element('ul');
 for(const model of models)list.append(element('li',model));
 details.append(element('summary',models.length+' 个模型'),list);
 return details;
}
const RESET_DAYS=['周日','周一','周二','周三','周四','周五','周六'];
const parseReset=text=>{
 const m=/^([0-6]) (\d{2}:\d{2})$/.exec(text||'');
 return m?[+m[1],m[2]]:[1,'00:00'];
};
const formatReset=text=>{const[d,t]=parseReset(text);return RESET_DAYS[d]+' '+t+(text?'':'（默认）');};
function keyForm(row,close){
 const [resetDay,resetTime]=parseReset(row.week_reset);
 const f=form([
  ['weekly','周限额 USD','number',(row.weekly||0)/1e9],
  ['total','总限额 USD','number',(row.total||0)/1e9],
  ['weekday','周重置 · 星期（北京时间）','select',String(resetDay),[['1','周一'],['2','周二'],['3','周三'],['4','周四'],['5','周五'],['6','周六'],['0','周日']]],
  ['weektime','周重置 · 时刻','time',resetTime],
  ['models','可调用模型','text',(row.models||[]).join(', ')]
 ],data=>api('keys',{method:'PUT',body:JSON.stringify({id:row.id,weekly:Math.round(data.weekly*1e9),total:Math.round(data.total*1e9),models:modelList(data.models),week_reset:data.weekday+' '+(data.weektime||'00:00')})}));
 f.classList.add('key-form');
 const allowed=f.querySelector('input[type=text]'),field=allowed.closest('label');
 allowed.placeholder='留空为不限；多个模型用逗号分隔，* 为通配';field.classList.add('wide');
 field.before(element('p','0 为不限。周限额按上面的重置时间每周重新计算（北京时间），不调整就是每周一 0 点；两项都设置时，先用完的一项生效，用完后该密钥的请求会被拒绝。','muted hint'));
 const actions=element('div',null,'actions');
 actions.append(f.querySelector('button.primary'),action('关闭',close));
 f.append(actions);
 api('models').then(list=>{
  if(!list.length)return;
  const chips=element('div',null,'chips');
  chips.append(element('small','CPA 当前模型，点击加入或移除：','muted'));
  const paint=()=>{const chosen=modelList(allowed.value);chips.querySelectorAll('button').forEach(b=>b.setAttribute('aria-pressed',String(chosen.includes(b.textContent))));};
  for(const model of list){
   const chip=element('button',model.id,'chip');chip.type='button';chip.disabled=allowed.disabled;
   chip.addEventListener('click',()=>{
    const chosen=modelList(allowed.value),at=chosen.indexOf(model.id);
    at<0?chosen.push(model.id):chosen.splice(at,1);
    allowed.value=chosen.join(', ');paint();
   });
   chips.append(chip);
  }
  allowed.addEventListener('input',paint);paint();
  field.after(chips);
 }).catch(()=>{});
 if(row.cpa_present===false)f.querySelectorAll('input,button.primary').forEach(n=>n.disabled=true);
 return f;
}
function timeSeries(points){
 const box=element('section',null,'panel');
 const heading=element('div',null,'chart-heading');
 const select=element('select');select.setAttribute('aria-label','趋势指标');
 for(const [key,label] of [['requests','请求数'],['tokens','Token'],['cost','已计价费用']]){
  const option=element('option',label);option.value=key;select.append(option);
 }
 heading.append(element('h2','用量趋势'),select);box.append(heading);
 if(points.length<2){
  select.disabled=true;box.append(element('div','所选时间内没有请求。','chart-empty'));return box;
 }
 // Hours within one day are labelled by the hour alone.
 const tick=points[0].bucket.slice(0,10)===points.at(-1).bucket.slice(0,10)?11:5;
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
  const formatCount=key==='tokens'?tokenCount:integer;
  const sum=values.reduce((a,b)=>a+b,0);
  canvas.setAttribute('aria-label',select.selectedOptions[0].textContent+'趋势，合计 '+(key==='cost'?usd(sum):formatCount(sum)));
  const axis=key==='tokens'?tokenCount:k=>key==='cost'?(k===0?'$0':'$'+(k/1e9).toPrecision(2)):new Intl.NumberFormat('zh-CN',{notation:'compact',maximumFractionDigits:1}).format(k);
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
  for(const i of labelAt){ctx.textAlign=i===0?'left':i===points.length-1?'right':'center';ctx.fillText(points[i].bucket.slice(tick),x(i),h-5);}
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
  readout.textContent=points[selected].bucket+' · '+(key==='cost'?usdSummary(values[selected]):formatCount(values[selected]));
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

function usageCards(rows,series,points){
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
  ['总 Token',tokenCount(tokens),'tokens','neutral',cached?'缓存命中 '+tokenCount(cached)+' Token':'输入、输出与缓存'],
  ['已计价费用',usdSummary(spend),'cost','neutral',pending?pending+' 笔待计价':'按模型价格估算'],
  ['缓存命中率',input?rate((cached||count(rows,'cached'))/input):'—','cacheRate','neutral',input?tokenCount(input)+' 输入 Token':'等待用量统计'],
  ['请求成功率',requests?rate((requests-fails)/requests):'—','successRate','live',integer(fails)+' 次失败'],
 ];
 const grid=element('div',null,'cards usage-cards');
 const value=(point,key)=>{
  if(key==='cacheRate')return point.input_tokens?point.cached/point.input_tokens:0;
  if(key==='successRate')return point.requests?(point.requests-(point.failures||0))/point.requests:0;
  return point[key]||0;
 };
 for(const [label,number,key,tone,note] of metrics){
  const card=element('div',null,'card kpi kpi-'+tone);
  card.append(element('small',label),element('strong',number),element('p',note,'kpi-note'));
  if(points.length>1){
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

// One period selection for the overview, the request list and the sessions.
// Periods follow Beijing days, like the rest of the page.
const DAY=86400000,SHIFT=8*3600000;
const query={period:'today',from:'',to:'',page:{overview:1,events:1,sessions:1,turns:1},size:{overview:20,events:50,sessions:50,turns:50}};
function range(){
 const midnight=Math.floor((Date.now()+SHIFT)/DAY)*DAY-SHIFT;
 const at=v=>{const n=Date.parse(v+':00+08:00');return Number.isFinite(n)?n:null;};
 switch(query.period){
  case 'today':return [midnight,null];
  case 'yesterday':return [midnight-DAY,midnight];
  case '7':return [midnight-6*DAY,null];
  case '30':return [midnight-29*DAY,null];
  case 'custom':return [at(query.from),at(query.to)];
 }
 return [null,null];
}
function ranged(path,extra){
 const [from,to]=range(),q=new URLSearchParams(extra);
 if(from!=null)q.set('from',from);
 if(to!=null)q.set('to',to);
 return path+'?'+q;
}
const paged=view=>ranged(view,{page:query.page[view],size:query.size[view]});
function periodFilter(summary){
 const bar=element('div',null,'filter');
 const period=element('select');period.setAttribute('aria-label','时间周期');
 for(const [value,label] of [['today','今天'],['yesterday','昨天'],['7','近 7 天'],['30','近 30 天'],['all','全部'],['custom','自定义时间']]){
  const option=element('option',label);option.value=value;period.append(option);
 }
 period.value=query.period;
 const restart=()=>{for(const view in query.page)query.page[view]=1;return load(active);};
 period.addEventListener('change',()=>{
  if(period.value==='custom'&&!query.from&&!query.to){
   // Start the custom period from the one on screen.
   const stamp=ms=>ms==null?'':new Date(ms+SHIFT).toISOString().slice(0,16);
   [query.from,query.to]=range().map(stamp);
  }
  query.period=period.value;restart();
 });
 bar.append(period);
 if(query.period==='custom'){
  const inputs=['from','to'].map(name=>{
   const input=element('input');input.type='datetime-local';input.value=query[name];
   input.setAttribute('aria-label',name==='from'?'开始时间（北京时间）':'结束时间（北京时间）');
   return input;
  });
  bar.append(inputs[0],element('span','至','muted'),inputs[1],action('查询',()=>{
   if(inputs.some(input=>!input.reportValidity()))return;
   if(inputs[0].valueAsNumber>=inputs[1].valueAsNumber){
    notice('结束时间必须晚于开始时间。',true);
    inputs[1].focus();return;
   }
   query.from=inputs[0].value;query.to=inputs[1].value;return restart();
  },'primary'));
 }
 if(summary)bar.append(element('span',summary,'muted filter-summary'));
 return bar;
}
function pager(view,total,show){
 const page=query.page[view],pages=Math.max(1,Math.ceil(total/query.size[view])),bar=element('div',null,'pager');
 const turn=(label,to)=>{const b=action(label,()=>{query.page[view]=to;return show();});b.disabled=to<1||to>pages||to===page;return b;};
 const size=element('select');size.setAttribute('aria-label','每页条数');
 for(const n of [20,50,100,200]){const option=element('option','每页 '+n+' 条');option.value=n;size.append(option);}
 size.value=query.size[view];
 size.addEventListener('change',()=>{
  query.size[view]=Number(size.value);query.page[view]=1;
  Promise.resolve().then(show).catch(error=>notice(error.message,true));
 });
 bar.append(size,turn('首页',1),turn('上一页',page-1),element('span','第 '+page+' / '+pages+' 页','muted'),turn('下一页',page+1),turn('末页',pages));
 return bar;
}
// The trend is drawn per hour for a period of up to two days, per day otherwise.
function chartUnit(){
 const [from,to]=range();
 return from!=null&&(to??Date.now())-from<=2*DAY?'hour':'day';
}
// buckets lays the series out over the whole period, with empty hours or days
// filled in. A period without a start, or a very long one, begins at the first request.
function buckets(series){
 if(!series.length)return [];
 const hourly=chartUnit()==='hour',step=hourly?3600000:DAY;
 const label=ms=>{const text=new Date(ms+SHIFT).toISOString();return hourly?text.slice(0,13).replace('T',' ')+':00':text.slice(0,10);};
 const [from,to]=range(),index=new Map(series.map(row=>[row.bucket,row]));
 const first=Date.parse((hourly?series[0].bucket.replace(' ','T')+':00':series[0].bucket+'T00:00:00')+'+08:00');
 const end=Math.min(to==null?Infinity:to-1,Date.now());
 let start=from??first;
 if((end-start)/step>400)start=Math.max(start,first);
 const origin=Math.floor((start+SHIFT)/step)*step-SHIFT,points=[];
 const point=at=>index.get(label(at))||{bucket:label(at),requests:0,tokens:0,cost:0};
 for(let at=origin;at<=end;at+=step)points.push(point(at));
 // A line needs two points.
 if(points.length===1)points.unshift(point(origin-step));
 return points;
}

const views={
 overview:(rows,series)=>{
  const sum=name=>rows.reduce((n,row)=>n+(row[name]||0),0);
  const all=sum('requests'),points=buckets(series);
  const charts=element('div',null,'chart-grid');
  charts.append(timeSeries(points),sharePanel(rows));
  const detail=element('div');
  const paint=()=>{
   const size=query.size.overview;
   query.page.overview=Math.min(query.page.overview,Math.max(1,Math.ceil(rows.length/size)));
   const start=(query.page.overview-1)*size;
   detail.replaceChildren(table(rows.slice(start,start+size),[['模型','model'],['API Key',r=>keyName(r.key_id)],['请求占比',r=>all?(100*r.requests/all).toFixed(1)+'%':'—'],['Token',r=>tokenCount(r.total)],['费用',r=>usd(r.cost)],['缓存 %','cache_pct'],['平均延迟 ms','latency'],['平均首 Token ms','ttft']]),
    pager('overview',rows.length,paint));
  };
  paint();
  return [periodFilter(),usageCards(rows,series,points),charts,element('h2','模型与 Key 明细','section-heading'),detail];
 },
 keys:rows=>{
  const editor=panel('编辑限制',element('div'));editor.id='key-editor';editor.hidden=true;
  const keyCell=r=>{
   const label=element('span',r.name||r.masked_key||keyName(r.id));
   if(r.name && r.masked_key && r.name!==r.masked_key)label.append(element('small',' · '+r.masked_key,'muted'));
   if((r.weekly&&r.spent_week>=r.weekly)||(r.total&&r.spent>=r.total))label.append(document.createTextNode(' '),state('已达限额','error'));
   return label;
  };
  const rowsTable=table(rows.filter(r=>r.cpa_present!==false),[
   ['密钥',keyCell],['周限额',r=>r.weekly?usd(r.weekly):'不限'],['总限额',r=>r.total?usd(r.total):'不限'],['周重置',r=>formatReset(r.week_reset)],['可调用模型',r=>allowedModelsCell(r.models)],
   ['本周消费',r=>{const s=element('span',usd(r.spent_week));s.title='窗口自 '+date(r.week_start)+' 开始';return s;}],['累计消费',r=>usd(r.spent)],
   ['操作',r=>action('编辑',()=>{
    editor.hidden=false;editor.replaceChildren(element('h2','编辑限制 · '+(r.name||r.masked_key||keyName(r.id))),keyForm(r,()=>{editor.hidden=true;}));
    editor.scrollIntoView({block:'nearest',behavior:'smooth'});
   })]
  ]);
  return [rowsTable,editor];
 },
 events:data=>{
  const billing=r=>{
   const s=state(r.blocked||(r.cost==null?'待计价':r.approx?'已结算 · 估算':'已结算'),r.blocked?'error':r.cost==null?'pending':'ok');
   if(!r.blocked&&r.cost==null)s.title='价格表中暂无该模型，同步到价格后自动结算';
   return s;
  };
  const result=r=>r.failed?state('失败'+(r.http_status>=400?' · '+r.http_status:''),'error'):state('成功','ok');
  return [periodFilter('共 '+integer(data.total)+' 条 · 失败 '+integer(data.failed)+' · 费用 '+usdSummary(data.cost)),table(data.rows,[['时间',r=>date(r.happened)],['API Key',r=>keyName(r.key_id)],['模型','model'],['输入',r=>tokenCount(r.input)],['输出',r=>tokenCount(r.output)],['缓存',r=>tokenCount(r.cached)],['费用',r=>usd(r.cost)],['耗时',r=>r.latency?(r.latency/1000).toFixed(1)+' s':'—'],['结果',result],['计费',billing],['会话',r=>r.turn_id?action('查看',()=>showTurn(r.turn_id)):'—']]),pager('events',data.total,()=>load('events'))];
 },
 sessions:data=>[periodFilter('共 '+integer(data.total)+' 个会话'),
  table(data.rows,[['最后更新',r=>date(r.started)],['API Key',r=>keyName(r.key_id)],['会话','session'],['轮次','turns'],['费用',r=>usd(r.cost)],['操作',r=>action('打开',()=>{query.page.turns=1;return showSession(r);},'row-action')]]),
  pager('sessions',data.total,()=>load('sessions'))],

};
async function load(page){
 const token=++epoch;active=page;notice();chartObserver?.disconnect();repaintCharts=[];
 document.title=names[page]+' · EXT';
 document.querySelector('#description').textContent=descriptions[page];
 document.querySelectorAll('nav button').forEach(b=>b.setAttribute('aria-current',String(b.dataset.page===page)));
 content.replaceChildren(element('p','正在加载…','muted'));
 try{
  await syncNames();
  const [rows,keys,series]=await Promise.all([
   api(page==='overview'?ranged('overview'):page==='keys'?page:paged(page)),
   page==='keys'?null:api('keys'),
   page==='overview'?api(ranged('series',{unit:chartUnit()})):[]
  ]);
  for(const key of keys||(page==='keys'?rows:[]))if(key.name)keyNames.set(key.id,key.name);
  if(token===epoch)content.replaceChildren(...views[page](rows,series));
 }catch(error){
  if(token===epoch){content.replaceChildren();notice(error.message,true);}
 }
}
let polling=false;
async function monitor(){
 if(document.hidden || polling)return;
 polling=true;
 const badge=document.querySelector('#health');
 try{
  const health=await api('health');
  const ok=!health.error&&!String(health.last_poll).startsWith('0001');
  badge.dataset.status=ok?'ok':'error';
  badge.title=health.error||'';
  badge.replaceChildren(element('span'),document.createTextNode(ok?'采集正常':health.error?'采集异常':'等待采集'));
   const keyInteraction=()=>document.querySelector('#key-editor:not([hidden]),details.allowed-models:is([open],:focus-within)');
   if(active==='keys' && !keyInteraction()){
    const token=epoch,rows=await api('keys');
    if(token===epoch && active==='keys' && !keyInteraction()){
     rows.forEach(k=>keyNames.set(k.id,k.name||k.masked_key));content.replaceChildren(...views.keys(rows));
    }
   }
 }catch{
  badge.dataset.status='error';badge.replaceChildren(element('span'),document.createTextNode('采集状态不可用'));
 }finally{polling=false;}
}
function startDashboard(){
 load(locationPage());
 monitor();
 document.addEventListener('visibilitychange',monitor);
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
 document.querySelector('#logout').addEventListener('click',async ()=>{
  try{await api('logout',{method:'POST',body:'{}'});location.reload();}
  catch(error){notice(error.message,true);}
 });
 window.addEventListener('hashchange',()=>load(locationPage()));
 startDashboard();
}
