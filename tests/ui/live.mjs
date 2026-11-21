import assert from 'node:assert/strict';
import fs from 'node:fs';
import {chromium} from '/work/node_modules/playwright/index.mjs';
const env=Object.fromEntries(fs.readFileSync('/secrets/env','utf8').split('\n').filter(s=>s.includes('=')).map(s=>[s.slice(0,s.indexOf('=')),s.slice(s.indexOf('=')+1)]));
const browser=await chromium.launch({args:['--no-sandbox']});
try{
 const page=await browser.newPage({viewport:{width:1440,height:975},locale:'zh-CN'});
 const faults=[],writes=[];
 page.on('pageerror',e=>faults.push(e.message));
 page.on('request',r=>{if(r.url().includes('/cpa-ext/api/')&&r.method()!=='GET')writes.push(r.url());});
 await page.goto('http://127.0.0.1:8482/management.html');
 await page.locator('input[type=password]').fill(env.CPA_MANAGEMENT_KEY);
 await page.locator('form button[type=submit]').click();
 await page.locator('a[href*="/plugin-pages/cpa-ext/"]').click();
 const frame=page.frameLocator('iframe[title="cpa-ext"]');
 await frame.locator('input[type=password]').fill(env.ADMIN_PASSWORD);
 await frame.getByRole('button',{name:'登录',exact:true}).click();
 await frame.locator('#content').filter({hasNotText:'正在加载…'}).waitFor();
 assert.equal(await frame.locator('#demo-toggle,#demo-note,#cpa-auth-link').count(),0);
 assert.equal(await frame.getByRole('button',{name:/演示数据/}).count(),0);
 assert.equal(await frame.getByRole('link',{name:/CPA 认证文件/}).count(),0);
 assert.equal(await frame.getByRole('button',{name:'凭证状态',exact:true}).count(),0);
 await frame.getByRole('button',{name:'Key 额度',exact:true}).click();
 await frame.getByRole('table').waitFor();
 assert.equal(await frame.getByRole('button',{name:'从 CPA 同步',exact:true}).count(),0);
 assert.equal(await frame.getByRole('link',{name:'在 CPA 管理 Key',exact:true}).count(),0);
 assert.equal(await frame.getByRole('button',{name:'模型价格',exact:true}).count(),0);
 assert.equal(await frame.locator('thead th').count(),6);
 assert.equal(await frame.locator('#key-editor').isVisible(),false);
 assert.equal(await frame.locator('input[type=password]').count(),0);
 const prices=await page.request.get('http://127.0.0.1:8482/cpa-ext/api/prices');
 assert.equal(prices.status(),200);assert.ok((await prices.json()).some(p=>p.is_reference));
 await page.waitForRequest(r=>r.url().endsWith('/cpa-ext/api/keys'),{timeout:12000});
 for(const nav of ['用量总览','Key 额度','消费流水','会话审计']){
  await frame.getByRole('button',{name:nav,exact:true}).click();
  await frame.locator('#content').filter({hasNotText:'正在加载…'}).waitFor();
  assert.equal(await frame.getByRole('button',{name:nav,exact:true}).getAttribute('aria-current'),'true');
 }
 await frame.getByRole('button',{name:'用量总览',exact:true}).click();
 await frame.getByRole('img',{name:/请求数趋势/}).waitFor();
 await page.screenshot({path:'/work/cpa-ext-synchronized.jpg',quality:70});
 await page.setViewportSize({width:390,height:844});
 assert.ok(await frame.locator('body').evaluate(b=>b.scrollWidth<=window.innerWidth+1));
 assert.equal(faults.length,0,faults.join('\n'));
 assert.ok(writes.every(u=>u.endsWith('/api/session')),writes.join('\n'));
 console.log('Live CPAMC: four tabs, compact quota table, silent refresh, retained internal prices, no demo or credential shortcut, and mobile layout passed');
}finally{await browser.close();}
