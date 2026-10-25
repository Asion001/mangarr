import { test, expect } from '@playwright/test';

// the Continue reading shelf is on the page at first paint on a return visit, so it never pushes the library down
test('shows the last Continue reading shelf at once while the current one loads',async({page})=>{
  let delay=0;
  await page.route('**/api/v1/**',async route=>{
    const p=new URL(route.request().url()).pathname;
    if(p.endsWith('/auth/status')) return route.fulfill({json:{authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:[]}}});
    if(p.endsWith('/reading/shelf')) {
      if(delay) await new Promise(r=>setTimeout(r,delay));
      return route.fulfill({json:{user:'Guest',items:[{seriesId:1,title:'Moonlight',coverUrl:'',read:2,total:10,page:0,lastReadAt:'2026-10-20T00:00:00Z',next:{chapterId:7,number:'3',available:true}}]}});
    }
    if(p.endsWith('/series/search')) return route.fulfill({json:{page:1,pageSize:36,total:0,totalSize:0,languages:[],genres:[],genreCounts:{},items:[]}});
    return route.fulfill({json:[]});
  });

  await page.goto('/');
  await expect(page.getByRole('heading',{name:/Continue reading/})).toBeVisible();

  delay=5000;
  await page.reload();
  await expect(page.getByRole('heading',{name:/Continue reading/})).toBeVisible({timeout:2000});
  await expect(page.getByRole('link',{name:'Read Moonlight ch. 3'})).toBeVisible();
});
