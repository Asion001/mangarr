import { test, expect, type Page } from '@playwright/test';

const fromSite={format:'mangarr-user-data',version:1,exported:'2026-10-25T10:00:00Z',user:'anna',titles:[
  {title:'Blue Lock',sources:[{sourceId:'9',name:'MangaLib',url:'/1--blue-lock'}],following:true,lists:['Читаю'],reached:{chapter:-1,page:3,chapterRef:'55'}},
  {title:'Kaiju No. 8',sources:[{sourceId:'9',name:'MangaLib',url:'/2--kaiju'}],following:true,lists:['В планах']},
  {title:'Dropped one',sources:[{sourceId:'9',name:'MangaLib',url:'/3--dropped'}],lists:['Брошено']},
]};

/** mockAccount serves the account page as a user who can request; it records what is sent. */
async function mockAccount(page:Page) {
  const sent:{account?:any,import?:any}={};
  await page.route('**/api/v1/**',async route=>{
    const p=new URL(route.request().url()).pathname;
    if(p.endsWith('/auth/status')) return route.fulfill({json:{authenticated:true,account:{kind:'user',id:1,username:'anna',permissions:['requests.create']}}});
    if(p.endsWith('/me/ui-preferences')) return route.fulfill({json:{locale:'en',mode:'reading',updatedAt:'2026-09-17T00:00:00Z'}});
    if(p.endsWith('/me/data/summary')) return route.fulfill({json:{titles:0,read:0,history:0,follows:0,settings:true}});
    if(p.endsWith('/me/data/accounts')) return route.fulfill({json:[{moduleId:3,sourceId:'9',name:'MangaLib',lang:'ru',field:'Profile link',secret:false,steps:['Open your profile on mangalib.me and copy its address.']}]});
    if(p.endsWith('/me/data/from-account')) { sent.account=route.request().postDataJSON(); return route.fulfill({json:fromSite}); }
    if(p.endsWith('/me/data/preview')) return route.fulfill({json:[
      {index:0,title:'Blue Lock',seriesId:4,seriesTitle:'Blue Lock',by:'source',read:0,following:true,lists:['Читаю'],reached:{chapter:12,page:3}},
      {index:1,title:'Kaiju No. 8',seriesId:0,read:0,following:true,lists:['В планах'],source:'MangaLib'},
      {index:2,title:'Dropped one',seriesId:0,read:0,following:false,lists:['Брошено'],source:'MangaLib'},
    ]});
    if(p.endsWith('/me/data/import')) { sent.import=route.request().postDataJSON(); return route.fulfill({json:{titles:1,progress:12,kept:0,missing:0,history:0,sessions:0,follows:1,settings:0,requested:1}}); }
    if(p.endsWith('/me/notifications')||p.endsWith('/me/notifications/schema')||p.endsWith('/me/library-accounts')||p.endsWith('/me/sessions')) return route.fulfill({json:[]});
    return route.fulfill({json:{}});
  });
  return sent;
}

test('brings a site library: progress for titles here, one request for the rest',async({page})=>{
  const sent=await mockAccount(page);
  await page.goto('/account');
  const card=page.locator('section',{has:page.getByRole('heading',{name:'Your data',exact:true})});
  await expect(card.getByText('Bring my library from another app or site')).toBeVisible();
  await expect(card.locator('input[type=password]')).toHaveCount(0); // a profile link isn't a secret
  await card.locator('form input').fill(' https://mangalib.me/ru/user/77 ');
  await card.getByRole('button',{name:'Read my lists'}).click();
  expect(sent.account).toEqual({moduleId:3,sourceId:'9',token:'https://mangalib.me/ru/user/77'});

  const dialog=page.getByRole('dialog',{name:'Import your data'});
  await expect(dialog.getByText('From MangaLib account · 3 titles')).toBeVisible();
  await expect(dialog.getByRole('row',{name:/Blue Lock/}).getByText('ch. 12, p. 3')).toBeVisible();
  await expect(dialog.getByText('Reading history and time')).toHaveCount(0);
  await dialog.getByRole('row',{name:/Dropped one/}).getByRole('checkbox',{name:'Ask for it'}).uncheck();
  await expect(dialog.getByText('1 titles not here go to the admins as one request, with your lists and progress.')).toBeVisible();
  await dialog.getByRole('button',{name:'Import 1 titles'}).click();
  await expect(page.getByText(/1 titles asked for/)).toBeVisible();
  expect(sent.import.options).toMatchObject({progress:true,follows:true,request:[1]});
});
