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
      return route.fulfill({json:{page:Number(url.searchParams.get('page')||1),pageSize:36,total:80,totalSize:1234,languages:['en','uk'],genres:['Romance','Action','Horror'],genreCounts:{Romance:4,Action:3,Horror:1},items:[{
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
  await panel.getByLabel('Reading').selectOption('reading');
  await panel.getByLabel('Monitoring').selectOption('no');
  await panel.getByLabel('Sort',{exact:true}).selectOption('latest');
  await panel.getByRole('button',{name:'Ascending'}).click();
  await expect.poll(()=>seen.some(value=>value.includes('reading=reading')&&value.includes('monitored=no')&&value.includes('sort=latest')&&value.includes('order=asc'))).toBe(true);
  await panel.getByLabel('Library').selectOption('2');
  await panel.getByLabel('Language').selectOption('uk');
  // genres: type to narrow the list, tick several, they must all match unless switched to any
  const genres=panel.getByRole('combobox',{name:'Genres and tags'});
  await genres.fill('rom');
  const list=panel.getByRole('listbox',{name:'Genres and tags'});
  await expect(list.getByRole('option')).toHaveCount(1);
  await list.getByRole('option',{name:/Romance/}).click();
  await genres.fill('');
  await expect(list.getByRole('option',{name:/Romance/})).toHaveAttribute('aria-selected','true');
  await genres.press('ArrowDown');
  await genres.press('Enter');
  await expect(panel.getByRole('button',{name:'Remove Action'})).toBeVisible();
  await expect.poll(()=>seen.some(value=>value.includes('genre=Romance&genre=Action')&&!value.includes('genreMatch'))).toBe(true);
  await panel.getByRole('radio',{name:'Any of them'}).click();
  await expect.poll(()=>seen.some(value=>value.includes('genreMatch=any'))).toBe(true);
  await genres.press('Escape');
  await expect(list).toHaveCount(0);
  await expect(panel).toBeVisible();
  await expect(page.getByRole('button',{name:'Remove filter Romance'})).toBeVisible();
  await expect(page.getByText('has any of these')).toBeVisible();
  await page.getByRole('combobox').last().selectOption('48');
  await expect.poll(()=>seen.some(value=>value.includes('reading=reading')&&value.includes('sort=latest')&&value.includes('rootFolderId=2')&&value.includes('language=uk')&&value.includes('genre=Romance')&&value.includes('pageSize=48'))).toBe(true);
  await page.getByRole('button',{name:'Next',exact:true}).click();
  await expect.poll(()=>seen.some(value=>value.includes('page=2'))).toBe(true);

  await page.goto('/');
  // remembered filters show as chips, each one clears on its own
  await expect(page.getByRole('button',{name:'Remove filter Started reading'})).toBeVisible();
  await expect(page.getByRole('button',{name:'Remove filter Not monitored'})).toBeVisible();
  await expect(page.getByRole('button',{name:'Remove filter /manga/uk'})).toBeVisible();
  await expect(page.getByRole('button',{name:'Remove filter Ukrainian'})).toBeVisible();
  await page.getByRole('button',{name:'Filters'}).click();
  await expect(page.locator('#library-filters').getByLabel('Sort',{exact:true})).toHaveValue('latest');
  await expect(page.locator('#library-filters').getByRole('button',{name:'Ascending'})).toHaveAttribute('aria-pressed','true');
  await page.keyboard.press('Escape');
  await expect(page.locator('#library-filters')).toHaveCount(0);
  await expect(page.getByRole('combobox').last()).toHaveValue('48');
  await expect.poll(()=>seen.some(value=>value.includes('reading=reading')&&value.includes('sort=latest')&&value.includes('rootFolderId=2')&&value.includes('language=uk')&&value.includes('pageSize=48')&&!value.includes('page=2'))).toBe(true);
  await page.getByRole('button',{name:'Remove filter Ukrainian'}).click();
  await expect(page.getByRole('button',{name:'Remove filter Ukrainian'})).toHaveCount(0);
  await page.getByRole('button',{name:'Clear all'}).click();
  await expect(page.getByRole('button',{name:/^Remove filter/})).toHaveCount(0);
});

test('an old Show link opens with its own field, and the table sorts by a header and picks columns',async({page})=>{
  const seen:string[]=[];
  await page.route('**/api/v1/**',async route=>{
    const url=new URL(route.request().url());
    const p=url.pathname;
    if(p.endsWith('/auth/status')) return route.fulfill({json:{authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:[]}}});
    if(p.endsWith('/rootfolders')) return route.fulfill({json:[{id:1,path:'/manga/en',language:'en'}]});
    if(p.endsWith('/reading/shelf')) return route.fulfill({json:{items:[]}});
    if(p.endsWith('/series/search')) {
      seen.push(url.search);
      return route.fulfill({json:{page:1,pageSize:36,total:1,totalSize:1234,languages:['en'],genres:[],genreCounts:{},items:[{
        id:1,title:'Moonlight',sortTitle:'moonlight',status:'ongoing',monitored:false,monitorNew:'all',rootFolderId:1,path:'Moonlight',profileId:1,language:'en',sourcePriorityMode:'inherit',readingDirection:'rtl',tags:[],metadata:{year:2019,genres:['Romance']},addOptions:{pending:false},addedAt:'2026-09-17T00:00:00Z',updatedAt:'2026-09-17T00:00:00Z',coverUrl:'',following:false,sourceCount:3,
        stats:{chapterCount:10,monitoredCount:10,fileCount:8,missingCount:2,cleanedCount:0,sizeOnDisk:1234,spaceSaved:0,lastChapter:10,readCount:2,inProgressCount:1}
      }]}});
    }
    return route.fulfill({json:[]});
  });
  await page.addInitScript(()=>{ if(!sessionStorage.getItem('seeded')){ sessionStorage.setItem('seeded','1'); localStorage.setItem('seriesView','table'); } });
  await page.goto('/?filter=missing');
  await expect(page.getByRole('button',{name:'Remove filter Missing chapters'})).toBeVisible();
  await expect.poll(()=>seen.some(value=>value.includes('downloads=missing')&&!value.includes('filter='))).toBe(true);

  const table=page.getByRole('table');
  await expect(table.getByRole('link',{name:'Moonlight'})).toBeVisible();
  await expect(table.getByRole('columnheader',{name:'Monitored'})).toBeVisible();
  await table.getByRole('button',{name:'Sort by Size'}).click();
  await expect.poll(()=>seen.some(value=>value.includes('sort=size')&&!value.includes('order='))).toBe(true);
  await table.getByRole('button',{name:'Sort by Size'}).click();
  await expect.poll(()=>seen.some(value=>value.includes('sort=size')&&value.includes('order=asc'))).toBe(true);

  await page.getByRole('button',{name:'Columns'}).click();
  await page.getByRole('checkbox',{name:'Year'}).check();
  await page.getByRole('checkbox',{name:'Sources'}).check();
  await page.getByRole('checkbox',{name:'Added'}).uncheck();
  await expect(table.getByRole('columnheader',{name:'Added'})).toHaveCount(0);
  await expect(table.getByRole('cell',{name:'2019'})).toBeVisible();
  await expect(table.getByRole('cell',{name:'3',exact:true})).toBeVisible();
  await page.reload();
  await expect(page.getByRole('table').getByRole('columnheader',{name:'Year'})).toBeVisible();
  await expect(page.getByRole('table').getByRole('columnheader',{name:'Added'})).toHaveCount(0);
});
