import { test, expect } from '@playwright/test';
test('processing growth has one positive sign and readable mobile cards',async({page})=>{
 await page.setViewportSize({width:390,height:844});
 await page.route('**/api/v1/**',async route=>{
  const p=new URL(route.request().url()).pathname;
  let body:unknown;
  if(p.endsWith('/auth/status'))body={authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:['admin']}};
  else if(p.endsWith('/processing'))body={engines:[],state:{},pending:0,failed:0,processed:1,spaceSaved:0,spaceAdded:5300000,netSpaceSaved:-5300000,pagesPerMinute:13/1046*60,etaSeconds:0,active:[],recent:[{seriesId:1,seriesTitle:"I'm Not a Soccer Genius!",chapter:'86',sizeOriginal:10000000,size:15300000,pages:13,seconds:1046,processedAt:new Date().toISOString()}]};
  else if(p.endsWith('/processing/history'))body=[];
  else {await route.fulfill({status:503,json:{detail:'Not part of this fixture'}});return;}
  await route.fulfill({json:body});
 });
 await page.goto('/system/status');
 const card=page.locator('article').filter({hasText:"I'm Not a Soccer Genius!"});
 await expect(card).toBeVisible();
 await expect(card).toContainText('+53%');
 await expect(card).toContainText('0.746 p/min');
 await expect(card).toContainText('17m 26s');
 await expect(card.getByText('Size',{exact:true})).toBeVisible();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test('processing queue pages through the complete materialized backlog',async({page})=>{
 const seen:string[]=[];
 await page.route('**/api/v1/**',async route=>{
  const url=new URL(route.request().url());
  const p=url.pathname;
  let body:unknown;
  if(p.endsWith('/auth/status'))body={authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:['admin']}};
  else if(p.endsWith('/queue')&&url.searchParams.get('kind')==='reprocess'){
   seen.push(url.search);
   const pageNumber=Number(url.searchParams.get('page')||1);
   body={revision:7,total:125,page:pageNumber,pageSize:100,counts:{queued:125},state:{paused:false,quiet:{}},items:[{
    id:pageNumber,kind:'reprocess',seriesId:1,seriesTitle:'Long Backlog',chapter:String(pageNumber),numberSort:pageNumber,sourceName:'',scanlator:'',
    status:'queued',priority:-100,rank:pageNumber*1024,progress:0,pagesDone:0,pagesTotal:0,attempt:0,isUpgrade:true,error:'',
    notBefore:'2026-09-23T00:00:00Z',createdAt:'2026-09-23T00:00:00Z',updatedAt:'2026-09-23T00:00:00Z'
   }]};
  }
  else if(p.endsWith('/queue'))body={total:0,state:{paused:false,quiet:{}},items:[],counts:{}};
  else if(p.endsWith('/reading/shelf'))body={items:[]};
  else if(p.endsWith('/health'))body={checks:[]};
  else {await route.fulfill({status:503,json:{detail:'Not part of this fixture'}});return;}
  await route.fulfill({json:body});
 });
 await page.goto('/activity/processing');
 await expect(page.getByRole('heading',{name:'Processing queue'})).toBeVisible();
 await expect(page.getByText('Page 1 of 2')).toBeVisible();
 await expect.poll(()=>seen.some(value=>value.includes('pageSize=100'))).toBe(true);
 await page.getByRole('button',{name:'Next',exact:true}).click();
 await expect(page.getByText('Page 2 of 2')).toBeVisible();
 await expect.poll(()=>seen.some(value=>value.includes('page=2'))).toBe(true);
});

test('clear backlog asks first, then drops the waiting chapters',async({page})=>{
 let cleared=false;
 await page.route('**/api/v1/**',async route=>{
  const p=new URL(route.request().url()).pathname;
  let body:unknown;
  if(p.endsWith('/auth/status'))body={authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:['admin']}};
  else if(p.endsWith('/processing/clear-backlog')){cleared=true;body={jobs:120,chapters:3000};}
  else if(p.endsWith('/processing'))body={engines:[],state:{},pending:cleared?0:3000,pendingPages:cleared?0:60000,failed:0,processed:1,spaceSaved:0,spaceAdded:0,netSpaceSaved:0,pagesPerMinute:0,etaSeconds:0,active:[],recent:[]};
  else if(p.endsWith('/processing/history'))body=[];
  else {await route.fulfill({status:503,json:{detail:'Not part of this fixture'}});return;}
  await route.fulfill({json:body});
 });
 await page.goto('/system/status');
 await page.getByRole('button',{name:'Clear backlog'}).click();
 const dialog=page.getByRole('dialog');
 await expect(dialog).toContainText('3000 chapters');
 expect(cleared).toBe(false);
 await dialog.getByRole('button',{name:'Clear backlog'}).click();
 await expect(page.getByText('Backlog cleared: 3000 chapters left as they are')).toBeVisible();
 expect(cleared).toBe(true);
 await expect(page.getByRole('button',{name:'Clear backlog'})).toHaveCount(0);
});
