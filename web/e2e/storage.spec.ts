import { test, expect } from '@playwright/test';

const GB=1024**3;
const summary={
 library:{bytes:412*GB,series:312,files:18240,pages:18240*41,saved:96*GB},
 roots:[
  {id:1,path:'/library/manga',language:'en',series:198,files:11904,bytes:268*GB,driveTotal:800*GB,driveFree:196*GB,drive:'a'},
  {id:2,path:'/library/manga-ru',language:'ru',series:86,files:5130,bytes:118*GB,driveTotal:800*GB,driveFree:196*GB,drive:'a'},
  {id:3,path:'/library/webtoons',language:'ko',series:28,files:1206,bytes:26*GB,driveTotal:124*GB,driveFree:6*GB,drive:'b'},
 ],
 largest:[{id:7,title:'A Very Long Series Title That Should Truncate Nicely In Narrow Columns',language:'en',files:1127,bytes:25*GB},{id:8,title:'Second',language:'en',files:200,bytes:12*GB}],
 byStatus:[{status:'ongoing',series:141,bytes:239*GB},{status:'completed',series:171,bytes:173*GB}],
 readFiles:11320,readBytes:249*GB,recycleFiles:412,recycleBytes:8*GB,recycleDays:30,imageBytes:GB,imageMaxBytes:2*GB,
};

test('storage page sums the library, its drives and the largest series',async({page})=>{
 await page.setViewportSize({width:390,height:844});
 await page.route('**/api/v1/**',async route=>{
  const p=new URL(route.request().url()).pathname;
  if(p.endsWith('/auth/status'))return route.fulfill({json:{authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:['admin']}}});
  if(p.endsWith('/system/storage'))return route.fulfill({json:summary});
  return route.fulfill({status:503,json:{detail:'Not part of this fixture'}});
 });
 await page.goto('/system/storage');
 await expect(page.getByRole('heading',{name:'Storage',exact:true})).toBeVisible();
 await expect(page.getByText('412 GB',{exact:true})).toBeVisible();
 await expect(page.getByText('312 series · 18,240 chapters')).toBeVisible();
 await expect(page.getByText('412 files, emptied after 30 days')).toBeVisible();
 await expect(page.getByText('same drive as manga')).toBeVisible();
 await expect(page.getByText('low space')).toHaveCount(1);
 await expect(page.getByRole('link',{name:'Second'})).toHaveAttribute('href','/series/8');
 await expect(page.getByText('The top 2 take 37 GB, 9% of the library.')).toBeVisible();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
