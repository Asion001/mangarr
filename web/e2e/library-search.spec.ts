import { test, expect } from '@playwright/test';

test('searches, scopes and paginates the library through the server query',async({page})=>{
  const seen:string[]=[];
  await page.route('**/api/v1/**',async route=>{
    const url=new URL(route.request().url());
    const p=url.pathname;
    if(p.endsWith('/auth/status')) return route.fulfill({json:{authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:[]}}});
    if(p.endsWith('/rootfolders')) return route.fulfill({json:[{id:1,path:'/manga/en',language:'en'},{id:2,path:'/manga/uk',language:'uk'}]});
    if(p.endsWith('/reading/shelf')) return route.fulfill({json:{items:[]}});
    if(p.endsWith('/series/search')) {
      seen.push(url.search);
      const title=url.searchParams.get('q')?'Moonlight Alternative':'Moonlight';
      return route.fulfill({json:{page:Number(url.searchParams.get('page')||1),pageSize:36,total:80,totalSize:1234,languages:['en','uk'],genres:['Romance','Action'],items:[{
        id:1,title,sortTitle:'moonlight',status:'ongoing',monitored:true,monitorNew:'all',rootFolderId:1,path:'Moonlight',profileId:1,language:'en',sourcePriorityMode:'inherit',readingDirection:'rtl',tags:[],metadata:{altTitles:['Luna']},addOptions:{pending:false},addedAt:'2026-09-17T00:00:00Z',updatedAt:'2026-09-17T00:00:00Z',coverUrl:'',following:false,
        stats:{chapterCount:10,monitoredCount:10,fileCount:8,missingCount:2,cleanedCount:0,sizeOnDisk:1234,spaceSaved:0,lastChapter:10,readCount:2,inProgressCount:1}
      }]}});
    }
    return route.fulfill({json:[]});
  });

  await page.goto('/');
  await expect(page.getByRole('heading',{name:'Series',exact:true})).toBeVisible();
  await page.getByPlaceholder('Search titles, alternative titles and genres…').fill('moon');
  await expect(page.getByText('Moonlight Alternative')).toBeVisible();
  await expect.poll(()=>seen.some(value=>value.includes('q=moon'))).toBe(true);
  await page.getByRole('button',{name:'Filters'}).click();
  const panel=page.locator('#library-filters');
  await panel.getByLabel('Show').selectOption('reading');
  await page.getByLabel('Sort').selectOption('latest');
  await panel.getByLabel('Library').selectOption('2');
  await panel.getByLabel('Language').selectOption('uk');
  await panel.getByLabel('Genres and tags').selectOption('Romance');
  await page.getByRole('combobox').last().selectOption('48');
  await expect.poll(()=>seen.some(value=>value.includes('filter=reading')&&value.includes('sort=latest')&&value.includes('rootFolderId=2')&&value.includes('language=uk')&&value.includes('genre=Romance')&&value.includes('pageSize=48'))).toBe(true);
  await page.getByRole('button',{name:'Next',exact:true}).click();
  await expect.poll(()=>seen.some(value=>value.includes('page=2'))).toBe(true);

  await page.goto('/');
  // remembered filters show as chips, each one clears on its own
  await expect(page.getByRole('button',{name:'Remove filter Started reading'})).toBeVisible();
  await expect(page.getByRole('button',{name:'Remove filter /manga/uk'})).toBeVisible();
  await expect(page.getByRole('button',{name:'Remove filter Ukrainian'})).toBeVisible();
  await expect(page.getByLabel('Sort')).toHaveValue('latest');
  await expect(page.getByRole('combobox').last()).toHaveValue('48');
  await expect.poll(()=>seen.some(value=>value.includes('filter=reading')&&value.includes('sort=latest')&&value.includes('rootFolderId=2')&&value.includes('language=uk')&&value.includes('pageSize=48')&&!value.includes('page=2'))).toBe(true);
  await page.getByRole('button',{name:'Remove filter Ukrainian'}).click();
  await expect(page.getByRole('button',{name:'Remove filter Ukrainian'})).toHaveCount(0);
  await page.getByRole('button',{name:'Clear all'}).click();
  await expect(page.getByRole('button',{name:/^Remove filter/})).toHaveCount(0);
});
