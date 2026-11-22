import assert from 'node:assert/strict';
import fs from 'node:fs';
import {chromium} from '/work/node_modules/playwright/index.mjs';
const base=process.env.CPA_EXT_UI_BASE||'http://127.0.0.1:8482';
const env=Object.fromEntries(fs.readFileSync(process.env.CPA_EXT_UI_ENV||'/secrets/env','utf8').split('\n').filter(s=>s.includes('=')).map(s=>[s.slice(0,s.indexOf('=')),s.slice(s.indexOf('=')+1)]));
const browser=await chromium.launch({args:['--no-sandbox']});
try{
 const page=await browser.newPage({viewport:{width:1440,height:975},locale:'zh-CN'});
 const faults=[],writes=[];
 page.on('pageerror',e=>faults.push(e.message));
 page.on('request',r=>{if(r.url().includes('/cpa-ext/api/')&&r.method()!=='GET')writes.push(r.url());});
 await page.goto(base+'/management.html');
 await page.locator('input[type=password]').fill(env.CPA_MANAGEMENT_KEY);
 await page.locator('form button[type=submit]').click();
 const stranger={['0'.repeat(64)]:'ui-check'};
 // Simulate a saved CPAMC display name for an ID outside this ledger.
 await page.evaluate(({base,names})=>{
  const pad=new TextEncoder().encode('cli-proxy-api-webui::secure-storage|'+location.host);
  const bytes=new TextEncoder().encode(JSON.stringify(names));
  const encoded=btoa(Array.from(bytes,(byte,i)=>String.fromCharCode(byte^pad[i%pad.length])).join(''));
  localStorage.setItem('api-key-names:v1:'+base,'enc::v2::'+encoded);
 },{base,names:stranger});
 const naming=page.waitForResponse(r=>r.url().endsWith('/cpa-ext/api/keys/names')&&r.status()===200);
 await page.locator('a[href*="/plugin-pages/cpa-ext/"]').click();
 const frame=page.frameLocator('iframe[title="EXT"]');
 await frame.locator('input[type=password]').fill(env.ADMIN_PASSWORD);
 await frame.getByRole('button',{name:'登录',exact:true}).click();
 await frame.locator('#content').filter({hasNotText:'正在加载…'}).waitFor();
 const named=await naming;
 assert.deepEqual(named.request().postDataJSON(),{base,names:stranger});
 assert.deepEqual(await named.json(),{named:{}});
 assert.equal(await frame.locator('#demo-toggle,#demo-note,#cpa-auth-link').count(),0);
 assert.equal(await frame.getByRole('button',{name:/演示数据/}).count(),0);
 assert.equal(await frame.getByRole('link',{name:/CPA 认证文件/}).count(),0);
 assert.equal(await frame.getByRole('button',{name:'凭证状态',exact:true}).count(),0);
 await frame.getByRole('button',{name:'密钥限制',exact:true}).click();
 await frame.getByRole('table').waitFor();
 assert.equal(await frame.getByRole('button',{name:'从 CPA 同步',exact:true}).count(),0);
 assert.equal(await frame.getByRole('link',{name:'在 CPA 管理 Key',exact:true}).count(),0);
 assert.equal(await frame.getByRole('button',{name:'模型价格',exact:true}).count(),0);
 for(const name of ['密钥','周限额','总限额','周重置','可调用模型','本周消费','累计消费','操作']){
  await frame.getByRole('columnheader',{name,exact:true}).waitFor();
 }
 assert.equal(await frame.locator('#key-editor').isVisible(),false);
 assert.equal(await frame.locator('input[type=password]').count(),0);
 const prices=await page.request.get(base+'/cpa-ext/api/prices');
 assert.equal(prices.status(),200);assert.ok((await prices.json()).some(p=>p.is_reference));
 await page.waitForRequest(r=>r.url().endsWith('/cpa-ext/api/keys'),{timeout:12000});
 for(const nav of ['用量总览','密钥限制','请求明细','会话审计']){
  await frame.getByRole('button',{name:nav,exact:true}).click();
  await frame.locator('nav button[aria-current=true]').filter({hasText:nav}).waitFor();
  await frame.locator('#content').filter({hasNotText:'正在加载…'}).waitFor();
 }
 assert.equal(await frame.locator('h1').textContent(),'EXT');
 const period=frame.getByLabel('时间周期'),settled=()=>frame.locator('#content').filter({hasNotText:'正在加载…'}).waitFor();
 const counted=async unit=>Number((await frame.locator('.filter-summary').textContent()).match(new RegExp('共 ([\\d,]+) '+unit))[1].replaceAll(',',''));
 await frame.getByRole('button',{name:'请求明细',exact:true}).click();
 await frame.getByText(/^第 1 \/ \d+ 页$/).waitFor();
 assert.equal(await period.inputValue(),'today');
 assert.deepEqual(await period.locator('option').allTextContents(),['今天','昨天','近 7 天','近 30 天','全部','自定义时间']);
 const today=await counted('条');
 await period.selectOption('all');
 await frame.getByText(/^第 1 \/ \d+ 页$/).waitFor();
 const all=await counted('条');
 assert.ok(all>0&&today<=all&&await frame.locator('tbody tr').count()===Math.min(all,50));
 assert.equal(await frame.getByRole('button',{name:'上一页',exact:true}).isDisabled(),true);
 if(all>50){
  const first=await frame.locator('tbody tr').first().textContent();
  await frame.getByRole('button',{name:'下一页',exact:true}).click();
  await frame.getByText(/^第 2 \/ \d+ 页$/).waitFor();
  assert.notEqual(await frame.locator('tbody tr').first().textContent(),first);
 }
 // The period is shared by the three lists; each of them pages.
 await frame.getByRole('button',{name:'会话审计',exact:true}).click();
 await frame.getByText(/^第 1 \/ \d+ 页$/).waitFor();
 assert.equal(await period.inputValue(),'all');
 const sessions=await counted('个会话');
 assert.equal(await frame.locator('tbody tr:not(:has(.empty-cell))').count(),Math.min(sessions,50));
 if(sessions){
  await frame.getByRole('button',{name:'打开',exact:true}).first().click();
  await frame.getByText(/^第 1 \/ \d+ 页$/).waitFor();
  await frame.getByRole('button',{name:'返回会话',exact:true}).click();
  await frame.getByText(/^第 1 \/ \d+ 页$/).waitFor();
 }
 for(const [choice,axis] of [['all',/^\d{4}-\d\d-\d\d · /],['today',/^\d{4}-\d\d-\d\d \d\d:00 · /]]){
  await frame.getByRole('button',{name:'用量总览',exact:true}).click();
  await settled();
  await period.selectOption(choice);
  await settled();
  await frame.getByText(/^第 1 \/ \d+ 页$/).waitFor();
  if(choice==='all'||today)assert.match(await frame.locator('.chart-readout').textContent(),axis);
 }
 await frame.getByRole('button',{name:'请求明细',exact:true}).click();
 await frame.getByText(/^第 1 \/ \d+ 页$/).waitFor();
 assert.equal(await counted('条'),today);
 await period.selectOption('custom');
 await settled();
 await frame.getByLabel('开始时间（北京时间）').fill('2020-01-02T00:00');
 await frame.getByLabel('结束时间（北京时间）').fill('2020-01-01T00:00');
 await frame.getByRole('button',{name:'查询',exact:true}).click();
 await frame.getByText('结束时间必须晚于开始时间。').waitFor();
 await frame.getByLabel('开始时间（北京时间）').fill('2019-12-31T00:00');
 await frame.getByRole('button',{name:'查询',exact:true}).click();
 await frame.getByText('共 0 条').waitFor();
 await period.selectOption('all');
 await settled();
 await frame.getByRole('button',{name:'密钥限制',exact:true}).click();
 await frame.getByRole('button',{name:'编辑',exact:true}).first().click();
 await frame.getByLabel(/可调用模型/).waitFor();
 await frame.getByLabel('周限额 USD').waitFor();
 await frame.getByLabel('总限额 USD').waitFor();
 const editor=frame.locator('#key-editor');
 const weekday=editor.locator('label').filter({hasText:'周重置 · 星期（北京时间）'}).locator('select');
 const weektime=editor.locator('label').filter({hasText:'周重置 · 时刻'}).locator('input');
 await weekday.waitFor();
 assert.equal(await weekday.locator('option').count(),7);
 assert.equal(await weektime.getAttribute('type'),'time');
 await weekday.selectOption('0');
 await weektime.fill('18:30');
 assert.equal(await weekday.inputValue(),'0');
 assert.equal(await weektime.inputValue(),'18:30');
 assert.equal(await frame.locator('#key-editor input[type=checkbox]').count(),0);
 assert.equal(await frame.getByText(/显示名称|扩展网关|日额度/).count(),0);
 await frame.locator('.chips button').first().waitFor();
 await frame.getByRole('button',{name:'关闭',exact:true}).click();
 await frame.getByRole('button',{name:'用量总览',exact:true}).click();
 await frame.getByRole('img',{name:/请求数趋势/}).waitFor();
 if(process.env.CPA_EXT_UI_SCREENSHOT)await page.screenshot({path:process.env.CPA_EXT_UI_SCREENSHOT,quality:70});
 await page.setViewportSize({width:390,height:844});
 assert.ok(await frame.locator('body').evaluate(b=>b.scrollWidth<=window.innerWidth+1));
 assert.equal(faults.length,0,faults.join('\n'));
 assert.ok(writes.every(u=>u.endsWith('/api/session')||u.endsWith('/api/keys/names')),writes.join('\n'));
 console.log('Live CPAMC: four tabs named EXT, key limits table with weekly and total limits, silent refresh, CPAMC key names read, retained internal prices, one period filter (today by default) and paging for overview, requests and sessions, model restriction editor, no demo or credential shortcut, and mobile layout passed');
}finally{await browser.close();}
