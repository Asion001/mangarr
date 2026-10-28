import { test, expect } from '@playwright/test';

const series=(id:number,title:string)=>({
  id,title,sortTitle:title.toLowerCase(),status:'ongoing',monitored:true,monitorNew:'all',rootFolderId:1,path:title,profileId:1,language:'en',sourcePriorityMode:'inherit',readingDirection:'rtl',tags:[],metadata:{},addOptions:{pending:false},addedAt:'2026-09-17T00:00:00Z',updatedAt:'2026-09-17T00:00:00Z',coverUrl:'',following:false,
  stats:{chapterCount:10,monitoredCount:10,fileCount:10,missingCount:0,cleanedCount:0,sizeOnDisk:1234,spaceSaved:0,lastChapter:10,readCount:0,inProgressCount:0},
});

test('puts the selected series in a status, a list, a new list, and takes them out of one',async({page})=>{
  const changes:unknown[]=[];
  let lists=[{id:1,name:'Favorites',kind:'favorites',createdAt:'2026-09-01T00:00:00Z',count:0},{id:5,name:'Old picks',kind:'',createdAt:'2026-09-01T00:00:00Z',count:3}];
  await page.addInitScript(()=>localStorage.setItem('mangarr:ui:anonymous:0',JSON.stringify({locale:'en',mode:'editing'})));
  await page.route('**/api/v1/**',async route=>{
    const req=route.request();
    const p=new URL(req.url()).pathname;
    if(p.endsWith('/auth/status')) return route.fulfill({json:{authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:['admin']}}});
    if(p.endsWith('/rootfolders')) return route.fulfill({json:[{id:1,path:'/manga/en',language:'en'}]});
    if(p.endsWith('/reading/shelf')) return route.fulfill({json:{items:[]}});
    if(p.endsWith('/series/search')) return route.fulfill({json:{page:1,pageSize:36,total:3,totalSize:3702,languages:['en'],genres:[],genreCounts:{},items:[series(1,'Moonlight'),series(2,'Sunrise'),series(3,'Starfall')]}});
    if(p.endsWith('/series/lists/bulk')){
      const body=req.postDataJSON();
      changes.push(body);
      return route.fulfill({json:{updated:body.seriesIds.length}});
    }
    if(p.endsWith('/me/lists')&&req.method()==='POST'){
      const made={id:9,name:req.postDataJSON().name,kind:'',createdAt:'2026-09-01T00:00:00Z',count:0};
      lists=[...lists,made];
      return route.fulfill({json:made});
    }
    if(p.endsWith('/me/lists')) return route.fulfill({json:{statuses:[],lists}});
    return route.fulfill({json:[]});
  });

  await page.goto('/');
  await page.getByRole('button',{name:'Filters'}).click();
  await page.getByRole('button',{name:'Select',exact:true}).click();
  await page.getByRole('checkbox',{name:/Moonlight/}).click();
  await page.getByRole('checkbox',{name:/Starfall/}).click();
  const bar=page.getByRole('toolbar',{name:'Selection'});

  await bar.getByRole('button',{name:'Add to list'}).click();
  await page.getByRole('menuitem',{name:'Completed'}).click();
  await expect(bar.getByText('Mark 2 series as Completed?')).toBeVisible();
  await bar.getByRole('button',{name:'Apply'}).click();
  await expect(page.getByText('Lists updated for 2 titles')).toBeVisible();

  await bar.getByRole('button',{name:'Add to list'}).click();
  await page.getByRole('menuitem',{name:'Old picks'}).last().click();
  await expect(bar.getByText('Take 2 series out of Old picks?')).toBeVisible();
  await bar.getByRole('button',{name:'Apply'}).click();
  await expect(bar.getByRole('button',{name:'Apply'})).toHaveCount(0);

  await bar.getByRole('button',{name:'Add to list'}).click();
  await page.getByRole('menuitem',{name:'New list…'}).click();
  await page.getByLabel('List name').fill('Summer');
  await page.getByRole('dialog').getByRole('button',{name:'Add'}).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);

  await expect.poll(()=>changes).toEqual([
    {seriesIds:[1,3],action:'status',status:'completed'},
    {seriesIds:[1,3],action:'remove',listId:5},
    {seriesIds:[1,3],action:'add',listId:9},
  ]);
});
