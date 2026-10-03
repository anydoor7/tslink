const fs = require('fs');
const cp = require('child_process');
const path = require('path');
const http = require('http');
const out = process.argv[2];
if (!out) throw Error('Usage: node scripts/check-portal-requests.cjs <artifact-directory>');
const pause = ms => new Promise(r => setTimeout(r, ms));
for(const name of ['normal','long-name']) { for(const suffix of ['.html','-headers.json']) fs.accessSync(path.join(out, 'portal-'+name+suffix)); }
const profile = fs.mkdtempSync(path.join(out, 'portal-chrome-'));
const log = fs.openSync(path.join(out, 'chrome.log'), 'w');
const server = http.createServer((req, res) => {
  const name = req.url.split('?')[0].slice(1);
  if (!['normal', 'long-name'].includes(name)) { res.writeHead(404); res.end(); return; }
  const headers = JSON.parse(fs.readFileSync(path.join(out, `portal-${name}-headers.json`)));
  for (const k of ['Content-Type', 'Content-Security-Policy', 'Cache-Control', 'X-Content-Type-Options', 'Referrer-Policy', 'X-Frame-Options']) {
    if (headers[k]) res.setHeader(k, headers[k]);
  }
  res.end(fs.readFileSync(path.join(out, `portal-${name}.html`)));
});
const child = cp.spawn(process.env.TSLINK_BROWSER || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', [
  '--headless', '--disable-gpu', '--disable-background-networking', '--password-store=basic', '--use-mock-keychain',
  '--no-first-run', '--remote-debugging-port=0', '--remote-debugging-address=127.0.0.1', `--user-data-dir=${profile}`, 'about:blank'
], {detached: true, stdio: ['ignore', log, log]});

const inspect = `(() => {
  const rgb = s => (s.match(/[\\d.]+/g) || []).slice(0,3).map(Number);
  const lum = c => rgb(c).map(n => { n/=255; return n<=0.04045?n/12.92:((n+0.055)/1.055)**2.4; }).reduce((a,n,i)=>a+n*[0.2126,0.7152,0.0722][i],0);
  const bg = e => { for(;e;e=e.parentElement) {const c=getComputedStyle(e).backgroundColor;if(c!=='rgba(0, 0, 0, 0)'&&c!=='transparent')return c;}return 'rgb(255,255,255)'; };
  const contrast = e => {const a=lum(getComputedStyle(e).color),b=lum(bg(e));return (Math.max(a,b)+0.05)/(Math.min(a,b)+0.05);};
  return {width:innerWidth,scrollWidth:document.documentElement.scrollWidth,height:document.documentElement.scrollHeight,
    dark:matchMedia('(prefers-color-scheme:dark)').matches,lang:document.documentElement.lang,
    cards:document.querySelectorAll('.app').length,heading:document.querySelector('h1').textContent,
    emptyHeading:document.querySelector('#empty-title')?.textContent,
    textContrast:[...document.querySelectorAll('h1,h2,p,a,.eyebrow,.status,footer')].map(e=>({text:e.textContent.trim(),ratio:contrast(e)})),
    durationOptions:[...document.querySelectorAll('select[name=duration] option')].map(e=>({value:e.value,label:e.textContent})),
    links:[...document.querySelectorAll('a')].map(e=>({text:e.innerText,href:e.getAttribute('href'),role:e.tagName})),
    overflow:[...document.querySelectorAll('p,a,h2,h3,.app,form,select,textarea')].filter(e=>e.scrollWidth>e.clientWidth+1).map(e=>({tag:e.tagName,text:e.textContent,width:e.clientWidth,scrollWidth:e.scrollWidth})),
    scripts:document.scripts.length,images:document.images.length};
})()`;

(async()=>{
 let ws;
 try {
  await new Promise(r=>server.listen(0,'127.0.0.1',r));
  const base = `http://127.0.0.1:${server.address().port}`;
  let port;
  for(let i=0;i<100;i++){try{port=fs.readFileSync(path.join(profile,'DevToolsActivePort'),'utf8').split('\n')[0];break;}catch{}await pause(100);}
  if(!port)throw Error('Chrome failed to start');
  const tab=await(await fetch(`http://127.0.0.1:${port}/json/new?about:blank`,{method:'PUT'})).json();
  ws=new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((resolve,reject)=>{ws.onopen=resolve;ws.onerror=reject;});
  let seq=0;const pending=new Map(),browserErrors=[];
  ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.id){const p=pending.get(m.id);pending.delete(m.id);m.error?p.reject(Error(JSON.stringify(m.error))):p.resolve(m.result);} else if(m.method==='Log.entryAdded'){browserErrors.push(m.params.entry);}};
  const call=(method,params={})=>new Promise((resolve,reject)=>{const id=++seq;pending.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}));});
  await call('Page.enable');await call('Log.enable');await call('Network.enable');await call('Network.setBlockedURLs',{urls:['https://*']});
  const results=[];
  for(const name of ['normal','long-name'])for(const width of [390])for(const mode of ['light','dark']){
   await call('Emulation.setDeviceMetricsOverride',{width,height:900,deviceScaleFactor:1,mobile:false});
   await call('Emulation.setEmulatedMedia',{features:[{name:'prefers-color-scheme',value:mode}]});
   await call('Page.navigate',{url:base+'/'+name});
   for(let i=0;i<100;i++){const r=await call('Runtime.evaluate',{expression:`document.readyState==='complete' && location.pathname==='/${name}'`,returnByValue:true});if(r.result.value)break;await pause(25);}
   await pause(120);
   await call('Runtime.evaluate',{expression: `(()=>{const e=document.querySelector('select[name=duration]');if(e)e.value='1h';})()`});
   const r=await call('Runtime.evaluate',{expression:inspect,returnByValue:true});
   if(r.exceptionDetails)throw Error(JSON.stringify(r.exceptionDetails));
   const metrics=r.result.value;
   if(metrics.width!==width||metrics.dark!==(mode==='dark'))throw Error('viewport/color mismatch');
   await call('Input.dispatchKeyEvent',{type:'keyDown',key:'Tab',code:'Tab',windowsVirtualKeyCode:9,nativeVirtualKeyCode:9});
   await call('Input.dispatchKeyEvent',{type:'keyUp',key:'Tab',code:'Tab',windowsVirtualKeyCode:9,nativeVirtualKeyCode:9});
   metrics.focus=(await call('Runtime.evaluate',{expression:`(()=>{const e=document.activeElement,c=getComputedStyle(e);return {tag:e.tagName,text:e.innerText,visible:e.matches(':focus-visible'),outline:c.outlineStyle,outlineWidth:c.outlineWidth,outlineColor:c.outlineColor,rect:e.getBoundingClientRect().toJSON(),statusRect:e.closest('.app')?.querySelector('.status')?.getBoundingClientRect().toJSON(),outlineTop:e.getBoundingClientRect().top-parseFloat(c.outlineOffset)-parseFloat(c.outlineWidth)}})()`,returnByValue:true})).result.value;
   const ax=await call('Accessibility.getFullAXTree');
   fs.writeFileSync(path.join(out,`ax-${name}-${width}-${mode}.json`),JSON.stringify(ax,null,2)+'\n');
   const img=await call('Page.captureScreenshot',{format:'png',captureBeyondViewport:true,clip:{x:0,y:0,width,height:metrics.height,scale:1}});
   const file=`portal-${name}-${width}-${mode}.png`;
   fs.writeFileSync(path.join(out,file),Buffer.from(img.data,'base64'));
   results.push({name,width,mode,file,...metrics});
   console.log(JSON.stringify({name,width,mode,scrollWidth:metrics.scrollWidth,minContrast:Math.min(...metrics.textContrast.map(v=>v.ratio)),focus:metrics.focus.tag,overflow:metrics.overflow.length}));
  }
  fs.writeFileSync(path.join(out,'screenshots.json'),JSON.stringify({results,browserErrors},null,2)+'\n');
  if(results.some(r=>r.scrollWidth>r.width || r.overflow.length)) throw Error('request layout overflow');
 } finally {
  ws?.close();server.close();try{process.kill(-child.pid,'SIGTERM')}catch{}await pause(300);fs.closeSync(log);fs.rmSync(profile,{recursive:true,force:true});
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
