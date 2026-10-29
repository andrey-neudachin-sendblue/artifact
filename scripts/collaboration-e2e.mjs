// Run from the repo: node scripts/collaboration-e2e.mjs /path/to/playwright
// Uses a disposable loopback server, database, token and browser context.
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, writeFile, rm, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createServer } from 'node:net';
import { randomBytes } from 'node:crypto';

const playwrightPath=process.argv[2];
if(!playwrightPath)throw new Error('Pass the installed playwright package directory as the first argument.');
const { chromium }=await import(pathToFileURL(join(resolve(playwrightPath),'index.mjs')));
const { expect }=await import(pathToFileURL(join(resolve(playwrightPath),'test.mjs')));
const root=fileURLToPath(new URL('..',import.meta.url));
const dir=await mkdtemp(join(tmpdir(),'artifact-collaboration-e2e-'));
const evidence=resolve(process.env.ARTIFACT_E2E_EVIDENCE || join(dir,'evidence'));
await mkdir(evidence,{recursive:true});
const token=randomBytes(32).toString('hex');
const cli=join(dir,'artifact'),daemon=join(dir,'artifactd');
const listener=createServer();
await new Promise(resolve=>listener.listen(0,'127.0.0.1',resolve));
const port=listener.address().port;
await new Promise(resolve=>listener.close(resolve));
const endpoint=`http://127.0.0.1:${port}`;
let server,browser,context;
const log=[];
const api=async(path,options={})=>{
 const response=await fetch(endpoint+path,options);
 return {status:response.status,body:await response.text(),headers:response.headers};
};
const run=(...args)=>execFileSync(cli,['--endpoint',endpoint,'--token',token,...args],{cwd:root,encoding:'utf8',env:{...process.env,ARTIFACT_CONFIG:join(dir,'absent-config')}}).trim();
const json=(...args)=>JSON.parse(run('--json',...args));
const upload=async(name,body)=>{const path=join(dir,name);await writeFile(path,body);return json('up','--collab',path);};
try{
 execFileSync('go',['build','-o',cli,'.'],{cwd:root,stdio:'pipe'});
 execFileSync('go',['build','-o',daemon,'./cmd/artifactd'],{cwd:root,stdio:'pipe'});
 server=spawn(daemon,[],{cwd:root,env:{...process.env,ARTIFACT_TOKEN:token,ARTIFACT_BASE_URL:endpoint,ARTIFACT_ADDR:`127.0.0.1:${port}`,ARTIFACT_DATA_DIR:join(dir,'data')}});
 server.stdout.on('data',chunk=>log.push(chunk.toString()));
 server.stderr.on('data',chunk=>log.push(chunk.toString()));
 await expect.poll(async()=>{try{return (await fetch(endpoint+'/healthz')).status}catch{return 0}},{timeout:10000}).toBe(200);
 browser=await chromium.launch({headless:true});
 context=await browser.newContext({viewport:{width:1440,height:1000}});
 await context.tracing.start({screenshots:true,snapshots:true,sources:true});
 const page=await context.newPage();
 const open=async url=>{await page.goto("about:blank");await page.goto(url)};
 const errors=[];page.on('pageerror',error=>errors.push(error.message));
 const sentAuth=[];page.on('request',request=>{if(request.headers().authorization)sentAuth.push(request.headers().authorization)});
 const original='# Launch plan\n\nThis explanation is unnecessarily technical.\n\n## Timing\n\nShip next week.\n';
 // Every upload has view/edit links; ordinary links never grant write access.
 const informativeFile=join(dir,'informative.md');await writeFile(informativeFile,'# Informative report\n\nRead only.\n');
 const informative=json('up',informativeFile);
 assert.ok(informative.edit_url.includes('/edit#key='));
 await open(informative.url+'/edit');
 await expect(page.getByText('This is a view-only link.',{exact:false})).toBeVisible();
 await expect(page.getByRole('button',{name:'Save new version'})).toBeHidden();
 await expect(page.getByRole('button',{name:'Send suggestion'})).toBeHidden();
 const denied=await api('/api/artifacts/'+informative.id+'/suggestions',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({version:1,message:'Unauthorised feedback'})});
 assert.equal(denied.status,401);
 const art=await upload('plan.md',original);
 const review=run('edit-link',art.id);
 assert.ok(review.startsWith(`${endpoint}/${art.id}/edit#key=`));
 await open(review);
 await expect(page.getByRole('heading',{name:'Suggest a change'})).toBeVisible();
 await expect(page.locator('#artifact-agent-instructions')).toBeHidden();
 const agentInstructions=JSON.parse(await page.locator('#artifact-agent-instructions').textContent());
 assert.equal(agentInstructions.artifact_id,art.id);
 assert.equal(agentInstructions.viewed_version,1);
 assert.ok(agentInstructions.instructions.some(line=>line.includes('owner explicitly approves')));
 assert.ok(!JSON.stringify(agentInstructions).includes(token));
 const discovery=await page.locator('link[rel="describedby"]').getAttribute('href');
 const discovered=JSON.parse((await api(discovery)).body);
 assert.equal(discovered.artifact_id,art.id);
 assert.ok(!await page.locator('body').innerText().then(text=>text.includes('latest_version_at_request')));
 const documentFrame=page.frameLocator('iframe[title="Artifact version 1"]');
 await expect(documentFrame.getByRole('heading',{name:'Launch plan'})).toBeVisible();
 assert.equal(await documentFrame.locator('body').evaluate(()=>document.compatMode),'CSS1Compat');
 // Simulate a real text selection inside the sandbox and its mouseup event.
 await documentFrame.getByText('This explanation is unnecessarily technical.').evaluate(element=>{
  const range=document.createRange();range.selectNodeContents(element);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);element.dispatchEvent(new MouseEvent('mouseup',{bubbles:true}));
 });
 await expect(page.getByLabel('Selected text')).toHaveValue('This explanation is unnecessarily technical.');
 await page.getByLabel('What should change?').fill('Use plain language and move shipping to Friday.');
 await page.getByLabel('Your name').fill('Recipient');
 await page.getByRole('button',{name:'Send suggestion'}).click();
 await expect(page.getByRole('status',{name:'Suggestion status'})).toContainText('sent for version 1');
 const inbox=json('suggestions',art.id);
 assert.equal(inbox.suggestions.length,1);
 const suggestion=inbox.suggestions[0];
 assert.equal(suggestion.quote,'This explanation is unnecessarily technical.');
 assert.equal(suggestion.author,'Recipient');
 assert.equal(suggestion.version,1);
 assert.equal((await api(`/${art.id}/raw`)).body,original);
 assert.equal((await api(`/api/artifacts/${art.id}/suggestions`)).status,401);
 assert.equal((await api(`/api/artifacts/${art.id}/proposals/fake/accept`,{method:'POST'})).status,401);
 const revision='# Launch plan\n\nHere is the plan in plain language.\n\n## Timing\n\nShip Friday.\n';
 await writeFile(join(dir,'revision.md'),revision);
 const proposal=json('propose',art.id,String(suggestion.id),join(dir,'revision.md'),'--filename','plan.md','--base-version','1');
 assert.equal((await api(`/${art.id}/raw`)).body,original);
 await open(proposal.preview_url);
 await expect(page.getByRole('heading',{name:'Review revision'})).toBeVisible();
 const candidateInstructions=JSON.parse(await page.locator('#artifact-agent-instructions').textContent());
 assert.equal(candidateInstructions.proposal.id,proposal.id);
 await expect(page.locator('#artifact-agent-instructions')).toBeHidden();
 await expect(page.frameLocator('iframe[title="Original version"]').getByText('Ship next week.')).toBeVisible();
 await expect(page.frameLocator('iframe[title="Proposed revision"]').getByText('Ship Friday.')).toBeVisible();
 await expect(page.getByText(`artifact accept ${art.id} ${proposal.id}`)).toBeVisible();
 await page.screenshot({path:join(evidence,'revision-preview.png'),fullPage:true});
 run('accept',art.id,proposal.id);
 assert.equal((await api(`/${art.id}/raw`)).body,revision);
 assert.equal((await api(`/${art.id}@1/raw`)).body,original);
 assert.equal(json('suggestions',art.id).suggestions.length,0);
 assert.equal(json('suggestions',art.id,'--all').suggestions[0].status,'addressed');
 await page.reload();await expect(page.getByText('This revision was published.')).toBeVisible();
 // A pinned old review page must continue to submit against version 1.
 await open(run('review',art.id,'--version','1'));
 await expect(page.getByText('You’re viewing an older version.')).toBeVisible();
 await page.getByLabel('What should change?').fill('Revisit this old wording.');
 await page.getByRole('button',{name:'Send suggestion'}).click();
 await expect(page.getByRole('status',{name:'Suggestion status'})).toContainText('sent for version 1');
 assert.equal(json('suggestions',art.id).suggestions[0].version,1);
 // A floating review page pins the loaded version even while an owner updates.
 await open(review);
 await expect(page.getByRole('heading',{name:'Suggest a change'})).toBeVisible();
 await writeFile(join(dir,'third.md'),'# Current version\n\nShip Monday.\n');
 run('update',art.id,join(dir,'third.md'));
 await page.getByLabel('What should change?').fill('Comment on what I loaded before the update.');
 await page.getByRole('button',{name:'Send suggestion'}).click();
 await expect(page.getByRole('status',{name:'Suggestion status'})).toContainText('sent for version 2');
 assert.equal(json('suggestions',art.id).suggestions.at(-1).version,2);
 // Responsive review, persistent form errors, and a working retry.
 await page.setViewportSize({width:390,height:844});
 await open(review);
 await expect(page.frameLocator('iframe[title="Artifact version 3"]').getByRole('heading',{name:'Current version'})).toBeVisible();
 await page.getByLabel('What should change?').fill('Please keep this feedback if the network fails.');
 await page.route('**/api/artifacts/*/suggestions',route=>route.abort('failed'));
 await page.getByRole('button',{name:'Send suggestion'}).click();
 await expect(page.getByRole('button',{name:'Send suggestion'})).toBeEnabled();
 await expect(page.getByLabel('What should change?')).toHaveValue('Please keep this feedback if the network fails.');
 await page.unroute('**/api/artifacts/*/suggestions');
 await page.getByRole('button',{name:'Send suggestion'}).click();
 await expect(page.getByRole('status',{name:'Suggestion status'})).toContainText('sent for version 3');
 await page.screenshot({path:join(evidence,'mobile-feedback.png'),fullPage:true});
 assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 // Uploaded HTML remains byte-identical and works in a sandboxed review.
 const html='<h1>HTML mockup</h1><p>Change this label</p><script>try{parent.document.body.dataset.escaped="yes"}catch{}</script>';
 const htmlArt=await upload('mockup.html',html);
 assert.equal((await api(`/${htmlArt.id}`)).body,html);
 await open(run('review',htmlArt.id));
 await expect(page.frameLocator('iframe[title="Artifact version 1"]').getByRole('heading',{name:'HTML mockup'})).toBeVisible();
 assert.equal(await page.locator('body').getAttribute('data-escaped'),null);
 // Dangerous-looking feedback remains inert in the owner's review shell.
 const xss='<img src=x onerror="document.body.dataset.xss=1">';
 await page.getByLabel('What should change?').fill(xss);await page.getByRole('button',{name:'Send suggestion'}).click();
 await expect(page.getByRole('status',{name:'Suggestion status'})).toContainText('sent for version 1');
 const hs=json('suggestions',htmlArt.id).suggestions[0];
 await writeFile(join(dir,'mockup.html'),'<h1>Revised mockup</h1>');
 const hp=json('propose',htmlArt.id,String(hs.id),join(dir,'mockup.html'),'--base-version','1');
 await open(hp.preview_url);await expect(page.getByText(xss,{exact:true})).toBeVisible();
 assert.equal(await page.locator('body').getAttribute('data-xss'),null);
 // Images retain a whole-artifact feedback fallback.
 const image=await upload('chart.svg','<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><rect width="200" height="100" fill="steelblue"/></svg>');
 await open(run('review',image.id));
 await page.getByLabel('Location').fill('Top-right legend');await page.getByLabel('What should change?').fill('Increase the legend size.');
 await page.getByRole('button',{name:'Send suggestion'}).click();await expect(page.getByRole('status',{name:'Suggestion status'})).toContainText('sent for version 1');
 assert.equal(json('suggestions',image.id).suggestions[0].location,'Top-right legend');
 // A plain-text source is escaped, selectable, and never executes markup.
 const logArt=await upload('trace.log','<script>parent.document.body.dataset.logXss=1</script>\nLog entry');
 await open(run('review',logArt.id));
 await expect(page.frameLocator('iframe[title="Artifact version 1"]').getByText('Log entry',{exact:false})).toBeVisible();
 assert.equal(await page.locator('body').getAttribute('data-log-xss'),null);
 // A real minimal PDF exercises the manual-location feedback path.
 const objects=['<< /Type /Catalog /Pages 2 0 R >>','<< /Type /Pages /Kids [3 0 R] /Count 1 >>','<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>','<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'];
 const stream='BT /F1 16 Tf 30 150 Td (PDF review) Tj ET';objects.push(`<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`);
 let pdf='%PDF-1.4\n';const offsets=[0];
 for(const [i,obj]of objects.entries()){offsets.push(Buffer.byteLength(pdf));pdf+=`${i+1} 0 obj\n${obj}\nendobj\n`;}
 const xref=Buffer.byteLength(pdf);pdf+=`xref\n0 6\n0000000000 65535 f \n${offsets.slice(1).map(n=>String(n).padStart(10,'0')+' 00000 n \n').join('')}trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF`;
 const pdfArt=await upload('notes.pdf',pdf);
 await open(run('review',pdfArt.id));await page.getByLabel('Location').fill('Page 1, title');
 await page.getByLabel('What should change?').fill('Make the title larger.');await page.getByRole('button',{name:'Send suggestion'}).click();
 await expect(page.getByRole('status',{name:'Suggestion status'})).toContainText('sent for version 1');
 assert.equal(json('suggestions',pdfArt.id).suggestions[0].location,'Page 1, title');
 // Proposals become stale after updates and cannot overwrite later edits.
 const remaining=json('suggestions',art.id).suggestions[0];
 const stale=json('propose',art.id,String(remaining.id),join(dir,'revision.md'),'--base-version','3');
 run('update',art.id,join(dir,'third.md'));
 const conflict=await api(`/api/artifacts/${art.id}/proposals/${stale.id}/accept`,{method:'POST',headers:{Authorization:`Bearer ${token}`}});
 assert.equal(conflict.status,409);
 await open(stale.preview_url);await expect(page.getByText(/This revision needs to be refreshed/)).toBeVisible();
 await expect(page.getByText(`artifact accept ${art.id} ${stale.id}`)).toHaveCount(0);
 // Source editing saves a new version, and a stale tab retains its draft.
 await page.setViewportSize({width:1440,height:1000});
 await open(informative.edit_url);
 await expect(page.getByLabel('Source',{exact:true})).toHaveValue('# Informative report\n\nRead only.\n');
 const second=await context.newPage();await second.goto(informative.edit_url);
 await expect(second.getByLabel('Source',{exact:true})).toBeVisible();
 await page.getByLabel('Source',{exact:true}).fill('# Edited in the browser\n');
 await page.getByRole('button',{name:'Save new version'}).click();
 await expect(page.getByRole('status',{name:'Save status'})).toContainText('Saved version 2');
 assert.equal((await api('/'+informative.id+'/raw')).body,'# Edited in the browser\n');
 await second.getByLabel('Source',{exact:true}).fill('# My retained draft\n');
 await second.getByRole('button',{name:'Save new version'}).click();
 await expect(second.getByRole('status',{name:'Save status'})).toContainText('Someone published a newer version');
 await expect(second.getByLabel('Source',{exact:true})).toHaveValue('# My retained draft\n');
 await second.close();
 // Uploading replacement bytes works for binary artifacts, too.
 await open(run('edit-link',pdfArt.id));
 await expect(page.getByLabel('Source',{exact:true})).toBeHidden();
 await page.getByLabel('Replace with a file').setInputFiles({name:'replacement.txt',mimeType:'text/plain',buffer:Buffer.from('Replacement content')});
 await page.getByRole('button',{name:'Save new version'}).click();
 await expect(page.getByRole('status',{name:'Save status'})).toContainText('Saved version 2');
 assert.equal((await api('/'+pdfArt.id+'/raw')).body,'Replacement content');
 // A copied edit key cannot edit another artifact; key rotation revokes old links.
 const oldKey=new URL(informative.edit_url).hash.slice(5);
 await open(art.url+'/edit#key='+oldKey);
 await expect(page.getByText('This edit key is invalid or has been revoked.',{exact:false})).toBeVisible();
 await expect(page.getByRole('button',{name:'Save new version'})).toBeHidden();
 const newLink=run('edit-link',informative.id,'--rotate');
 await open(informative.edit_url);
 await expect(page.getByText('This edit key is invalid or has been revoked.',{exact:false})).toBeVisible();
 await open(newLink);
 await expect(page.getByLabel('Source',{exact:true})).toHaveValue('# Edited in the browser\n');
 await page.screenshot({path:join(evidence,'keyed-editor.png'),fullPage:true});
 // A fresh recipient context given only the view link has no editor access.
 const visitor=await browser.newContext();const viewPage=await visitor.newPage();await viewPage.goto(informative.url+'/edit');
 await expect(viewPage.getByRole('button',{name:'Save new version'})).toBeHidden();
 await visitor.close();
 assert.equal(errors.length,0,`Browser errors: ${errors.join('; ')}`);
 assert.ok(!sentAuth.includes('Bearer '+token),'The browser must never receive the owner bearer token');
 await context.tracing.stop({path:join(evidence,'browser-trace.zip')});
 console.log(JSON.stringify({result:'PASS',scenarios:['view-only links reject writes','every artifact has an edit link','source editing','binary file replacement','stale save retains draft','cross-artifact key rejection','revoked link rejection','fresh visitor cannot edit','browser selection and feedback','CLI inbox','isolated before/after preview','owner accept','frozen versions','version pinning across concurrent update','old-version feedback','mobile feedback and network retry','byte-identical HTML','sandbox isolation','inert feedback markup','image location fallback','plain-text escaping','PDF feedback fallback','standards-mode rendering','stale publication refusal','no owner credentials in browser','hidden agent workflow metadata and JSON discovery'],evidence},null,2));
}catch(error){
 if(context){await context.tracing.stop({path:join(evidence,'failure-trace.zip')}).catch(()=>{});}
 await writeFile(join(evidence,'server.log'),log.join(''));
 throw error;
}finally{
 if(browser)await browser.close();
 if(server){server.kill('SIGTERM');await new Promise(resolve=>{if(server.exitCode!==null)return resolve();server.once('exit',resolve)});}
 // Evidence outside dir is preserved; otherwise report location and retain it.
 if(!evidence.startsWith(dir+'/'))await rm(dir,{recursive:true,force:true});
}
