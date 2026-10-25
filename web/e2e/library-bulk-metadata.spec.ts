import { test, expect } from '@playwright/test';

const series=(id:number,title:string)=>({
  id,title,sortTitle:title.toLowerCase(),status:'ongoing',monitored:true,monitorNew:'all',rootFolderId:1,path:title,profileId:1,language:'en',sourcePriorityMode:'inherit',readingDirection:'rtl',tags:[],metadata:{},addOptions:{pending:false},addedAt:'2026-09-17T00:00:00Z',updatedAt:'2026-09-17T00:00:00Z',coverUrl:'',following:false,
  stats:{chapterCount:10,monitoredCount:10,fileCount:10,missingCount:0,cleanedCount:0,sizeOnDisk:1234,spaceSaved:0,lastChapter:10,readCount:0,inProgressCount:0},
});

test('refreshes the metadata of the selected series in one task',async({page})=>{
  const commands:unknown[]=[];
  await page.addInitScript(()=>localStorage.setItem('mangarr:ui:anonymous:0',JSON.stringify({locale:'en',mode:'editing'})));
  await page.route('**/api/v1/**',async route=>{
    const req=route.request();
    const p=new URL(req.url()).pathname;
    if(p.endsWith('/auth/status')) return route.fulfill({json:{authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:['admin']}}});
    if(p.endsWith('/rootfolders')) return route.fulfill({json:[{id:1,path:'/manga/en',language:'en'}]});
    if(p.endsWith('/reading/shelf')) return route.fulfill({json:{items:[]}});
    if(p.endsWith('/series/search')) return route.fulfill({json:{page:1,pageSize:36,total:3,totalSize:3702,languages:['en'],genres:[],genreCounts:{},items:[series(1,'Moonlight'),series(2,'Sunrise'),series(3,'Starfall')]}});
    if(p.endsWith('/commands')&&req.method()==='POST'){
      commands.push(req.postDataJSON());
      return route.fulfill({json:{id:1,name:'RefreshMetadata',body:{},status:'queued',trigger:'manual',message:'',queuedAt:'2026-10-25T00:00:00Z',durationMs:0,error:''}});
    }
    return route.fulfill({json:[]});
  });

  await page.goto('/');
  await page.getByRole('button',{name:'Filters'}).click();
  await page.getByRole('button',{name:'Select',exact:true}).click();
  await page.getByRole('checkbox',{name:/Moonlight/}).click();
  await page.getByRole('checkbox',{name:/Starfall/}).click();
  const bar=page.getByRole('toolbar',{name:'Selection'});
  await bar.getByRole('button',{name:'More'}).click();
  await page.getByRole('menuitem',{name:'Refresh metadata'}).click();
  await expect(page.getByText('Refreshing metadata of 2 series')).toBeVisible();
  expect(commands).toEqual([{name:'RefreshMetadata',body:{seriesIds:[1,3]}}]);
});
