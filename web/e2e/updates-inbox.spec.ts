import { expect, test } from '@playwright/test';

const item=(id:number,seen:boolean,at:string,title='Tin Rivers')=>({kind:'chapter',at,seriesId:1,seriesTitle:title,chapterId:id,number:String(id),coverUrl:'',readable:true,readState:'unread',seen});

test('new updates are marked, counted in the menu and can be marked seen without reading',async({page})=>{
 let seenBodies:unknown[]=[];
 let left=2;
 await page.route('**/api/v1/**',async route=>{
  const url=new URL(route.request().url());
  const p=url.pathname;
  if(p.endsWith('/auth/status'))return route.fulfill({json:{authenticated:true,account:{kind:'user',id:2,username:'reader',permissions:[]}}});
  if(p.endsWith('/updates/unseen'))return route.fulfill({json:{count:left}});
  if(p.endsWith('/updates/seen')){seenBodies.push(route.request().postDataJSON());left=0;return route.fulfill({json:{count:0}});}
  if(p.endsWith('/updates')){
   const items=left?[item(3,false,'2026-10-20T10:00:00Z'),item(2,false,'2026-10-20T09:00:00Z'),item(1,true,'2026-10-19T09:00:00Z')]:[item(3,true,'2026-10-20T10:00:00Z'),item(2,true,'2026-10-20T09:00:00Z'),item(1,true,'2026-10-19T09:00:00Z')];
   return route.fulfill({json:{items,total:3,page:1,pageSize:50,unseen:left,seenThrough:left?'2026-10-19T12:00:00Z':'2026-10-20T10:00:00Z'}});
  }
  return route.fulfill({json:[]});
 });
 await page.goto('/updates');
 await expect(page.getByText('2 new since you last looked.')).toBeVisible();
 await expect(page.getByRole('link',{name:/Updates/}).first()).toContainText('2');
 await expect(page.getByRole('separator')).toContainText('Seen up to here');
 await page.getByRole('button',{name:'Mark Tin Rivers chapter 3 seen'}).click();
 expect(seenBodies.at(-1)).toMatchObject({items:[{kind:'chapter',id:3}]});
 await expect(page.getByRole('button',{name:/Mark all/})).toHaveCount(0);
 await expect(page.getByRole('button',{name:/^Mark .* seen$/})).toHaveCount(0);
});

test('filters live in the address and "mark these" sends them',async({page})=>{
 const bodies:Record<string,unknown>[]=[];
 const queries:string[]=[];
 await page.route('**/api/v1/**',async route=>{
  const url=new URL(route.request().url());
  const p=url.pathname;
  if(p.endsWith('/auth/status'))return route.fulfill({json:{authenticated:true,account:{kind:'user',id:2,username:'reader',permissions:[]}}});
  if(p.endsWith('/updates/unseen'))return route.fulfill({json:{count:1}});
  if(p.endsWith('/updates/seen')){bodies.push(route.request().postDataJSON());return route.fulfill({json:{count:0}});}
  if(p.endsWith('/updates')){queries.push(url.search);return route.fulfill({json:{items:[item(5,false,'2026-10-20T10:00:00Z')],total:1,page:1,pageSize:50,unseen:1}});}
  return route.fulfill({json:[]});
 });
 await page.goto('/updates?availability=downloaded&followed=1');
 await expect(page.getByText('Some updates are hidden by the filters.')).toBeVisible();
 expect(queries.at(-1)).toContain('availability=downloaded');
 expect(queries.at(-1)).toContain('followed=true');
 await page.getByRole('button',{name:'Mark these 1 seen'}).click();
 await expect.poll(()=>bodies.length).toBe(1);
 expect(bodies[0]).toMatchObject({all:true,availability:'downloaded',followed:true});
 await page.getByRole('button',{name:'Clear filters'}).click();
 await expect(page).not.toHaveURL(/availability/);
});
