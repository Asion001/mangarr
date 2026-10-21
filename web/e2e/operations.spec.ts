import { test, expect } from '@playwright/test';

const now=Date.now();
const attention={
 failedDownloads:{count:3,sources:[{source:'1:ck',name:'Comick',count:2},{source:'1:md',name:'MangaDex',count:1}]},
 sources:{unhealthy:[{source:'1:ck',name:'Comick',cooldownUntil:new Date(now+40*60e3).toISOString(),reason:'rate limited (429)',requests:120,failures:20}]},
 backups:{count:2,latestAt:new Date(now-3600e3).toISOString(),latestName:'b.zip',latestVerified:false,lastVerifiedAt:new Date(now-86400e3).toISOString()},
 workers:{enabled:2,stale:[{id:2,name:'upscaler-2',lastSeenAt:new Date(now-26*60e3).toISOString()}]},
 processing:{failed:0,running:3,waiting:12},
 disk:{path:'/library/manga',free:196*1024**3,total:800*1024**3,low:false,folders:2},
};
const points=Array.from({length:24},(_,i)=>({at:new Date(now-(23-i)*3600e3).toISOString(),requests:50,failures:i>20?20:1}));
const reliability=[
 {moduleId:1,sourceId:'md',name:'MangaDex',lang:'en',moduleName:'Suwayomi',enabled:true,requests:4812,failures:30,throttled:0,fallbacks:0,medianMs:600,p90Ms:1400,series:141,failingSeries:0,points,recentFailures:[]},
 {moduleId:1,sourceId:'ck',name:'Comick',lang:'en',moduleName:'Suwayomi',enabled:true,requests:120,failures:20,throttled:12,fallbacks:11,medianMs:1900,p90Ms:6400,series:64,failingSeries:2,
  cooldownUntil:new Date(now+40*60e3).toISOString(),cooldownReason:'rate limited (429)',points,recentFailures:[{at:new Date(now-60e3).toISOString(),what:'Page 14 image',error:'HTTP error 429'}]},
];

async function mock(page:import('@playwright/test').Page){
 await page.route('**/api/v1/**',async route=>{
  const p=new URL(route.request().url()).pathname;
  if(p.endsWith('/auth/status'))return route.fulfill({json:{authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:['admin']}}});
  if(p.endsWith('/system/attention'))return route.fulfill({json:attention});
  if(p.endsWith('/health'))return route.fulfill({json:{checkedAt:new Date().toISOString(),checks:[{source:'Downloads',type:'warning',message:'3 downloads failed in the last 24 hours',link:'/activity/downloads?status=failed',action:'Show 3 failed downloads'}]}});
  if(p.endsWith('/catalogs/reliability'))return route.fulfill({json:reliability});
  if(p.endsWith('/modules'))return route.fulfill({json:[{id:1,kind:'source',implementation:'suwayomi',name:'Suwayomi',enabled:true,capabilities:[],priority:25,tags:[],settings:{},fields:[]}]});
  return route.fulfill({status:503,json:{detail:'Not part of this fixture'}});
 });
}

test('status shows what needs attention and links each warning to its list',async({page})=>{
 await page.setViewportSize({width:390,height:844});
 await mock(page);
 await page.goto('/system/status');
 await expect(page.getByRole('heading',{name:/Needs attention/})).toContainText('4 of 6 areas');
 await expect(page.getByText('2 from Comick, 1 from MangaDex · last 24 hours')).toBeVisible();
 await expect(page.getByRole('link',{name:/Unhealthy sources/})).toHaveAttribute('href','/sources/reliability?source=1%3Ack');
 await expect(page.getByRole('link',{name:/Open workers/})).toContainText('1 stopped');
 await expect(page.getByRole('link',{name:'Show 3 failed downloads'})).toHaveAttribute('href','/activity/downloads?status=failed');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test('reliability lists sources and opens one in detail',async({page})=>{
 await mock(page);
 await page.goto('/sources/reliability');
 const row=page.getByRole('row',{name:/Comick/});
 await expect(row).toContainText('83%');
 await expect(row).toContainText('Cooling down');
 await page.getByRole('button',{name:'Comick'}).click();
 await expect(page).toHaveURL(/source=1%3Ack/);
 await expect(page.getByText('20 of 120 requests failed')).toBeVisible();
 await expect(page.getByText('HTTP error 429')).toBeVisible();
 await expect(page.getByRole('link',{name:'Show its failed downloads'})).toHaveAttribute('href','/activity/downloads?status=failed&source=1%3Ack');
});
