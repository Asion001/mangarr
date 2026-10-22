import { expect, test } from '@playwright/test';

const item=(id:number,title:string,extra:Record<string,unknown>={})=>({kind:'chapter',at:'2026-10-20T10:00:00Z',seriesId:id,seriesTitle:title,chapterId:id,number:String(id),coverUrl:'',language:'en',readable:true,downloaded:true,readState:'unread',seen:false,...extra});

test.use({viewport:{width:390,height:844}});

test('on a phone an update is one compact tile with its actions under the title',async({page},info)=>{
 await page.route('**/api/v1/**',async route=>{
  const p=new URL(route.request().url()).pathname;
  if(p.endsWith('/auth/status'))return route.fulfill({json:{authenticated:true,account:{kind:'user',id:2,username:'reader',permissions:[]}}});
  if(p.endsWith('/updates/unseen'))return route.fulfill({json:{count:3}});
  if(p.endsWith('/updates'))return route.fulfill({json:{items:[
   item(258,'The Beginning After the End'),
   item(41,'A Very Long Series Title That Would Wrap Across Several Lines On A Phone',{downloaded:false,readState:'in_progress',readPage:12}),
   {kind:'series',at:'2026-10-20T08:00:00Z',seriesId:9,seriesTitle:'Tin Rivers',coverUrl:'',languages:['ja','en'],seen:false},
  ],total:3,page:1,pageSize:50,unseen:3}});
  return route.fulfill({json:[]});
 });
 await page.goto('/updates');
 const read=page.getByRole('link',{name:'Read'});
 await expect(read).toBeVisible();
 const seen=page.getByRole('button',{name:'Mark The Beginning After the End chapter 258 seen'});
 await expect(seen).toBeVisible();
 await expect(page.getByText('Chapter 258 · ',{exact:false})).toBeVisible();
 await expect(page.getByRole('link',{name:'Continue'})).toBeVisible();
 await expect(page.getByRole('link',{name:'View'})).toBeVisible();
 await expect(page.getByText('New chapter').first()).toBeHidden();
 // the badges, title and button share the width instead of stacking
 // one per line beside a column of buttons
 for(const name of ['Read','Continue','View']){
  const tile=page.getByRole('link',{name}).locator('xpath=ancestor::div[contains(@class,"p-3")][1]');
  expect((await tile.boundingBox())!.height).toBeLessThan(160);
 }
 await page.screenshot({path:info.outputPath('updates-phone.png'),fullPage:true});
});
