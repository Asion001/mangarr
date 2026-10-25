import { test, expect, type Page } from '@playwright/test';

const exported={format:'mangarr-user-data',version:1,exported:'2026-10-19T10:00:00Z',user:'anna',titles:[
  {title:'Blue Period',sources:[{sourceId:'123',url:'/manga/blue'}],progress:[{number:'1',completed:true,at:'2026-10-01T00:00:00Z'}]},
  {title:'Kaguya-sama',progress:[{number:'1',completed:true,at:'2026-10-01T00:00:00Z'}]},
  {title:'Dandadan',following:true,progress:[{number:'1',completed:true,at:'2026-10-01T00:00:00Z'}]},
]};

/** mockAccount serves the account page; it records the import request. */
async function mockAccount(page:Page) {
  const sent:{body?:any}={};
  await page.route('**/api/v1/**',async route=>{
    const p=new URL(route.request().url()).pathname;
    if(p.endsWith('/auth/status')) return route.fulfill({json:{authenticated:true,account:{kind:'user',id:1,username:'anna',permissions:['requests.create']}}});
    if(p.endsWith('/me/ui-preferences')) return route.fulfill({json:{locale:'en',mode:'reading',updatedAt:'2026-09-17T00:00:00Z'}});
    if(p.endsWith('/me/data/summary')) return route.fulfill({json:{titles:312,read:9104,history:9406,follows:46,settings:true}});
    if(p.endsWith('/me/data/preview')) {
      const picked=route.request().postDataJSON().options?.picked??{};
      const m=[
        {index:0,title:'Blue Period',seriesId:4,seriesTitle:'Blue Period',by:'source',read:1,following:false},
        {index:1,title:'Kaguya-sama',seriesId:5,seriesTitle:'Kaguya Sama',by:'title',read:1,following:false},
        {index:2,title:'Dandadan',seriesId:0,read:1,following:true},
      ];
      if('1' in picked) Object.assign(m[1],{seriesId:0,seriesTitle:undefined,by:''});
      return route.fulfill({json:m});
    }
    if(p.endsWith('/me/data/import')) {
      sent.body=route.request().postDataJSON();
      return route.fulfill({json:{titles:1,progress:1,kept:0,missing:0,history:0,sessions:0,follows:0,settings:0}});
    }
    if(p.endsWith('/series')) return route.fulfill({json:[{id:4,title:'Blue Period'},{id:5,title:'Kaguya Sama'}]});
    if(p.endsWith('/me/notifications')||p.endsWith('/me/notifications/schema')||p.endsWith('/me/library-accounts')||p.endsWith('/me/sessions')) return route.fulfill({json:[]});
    return route.fulfill({json:{}});
  });
  return sent;
}

test('imports your data after a review of what matches here',async({page})=>{
  const sent=await mockAccount(page);
  await page.goto('/account');
  const card=page.locator('section',{has:page.getByRole('heading',{name:'Your data',exact:true})});
  await expect(card.getByText('Progress in 312 titles, down to the page')).toBeVisible();
  await expect(card.getByRole('link',{name:'Export my data'})).toHaveAttribute('href',/api\/v1\/me\/data$/);

  await card.locator('input[type=file]').first().setInputFiles({name:'data.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(exported))});
  const dialog=page.getByRole('dialog',{name:'Import your data'});
  await expect(dialog.getByText('Exported by anna')).toBeVisible();
  await expect(dialog.getByRole('row',{name:/Dandadan/}).getByText('Not on this server')).toBeVisible();
  await expect(dialog.getByRole('link',{name:'Request it'})).toBeVisible();

  // skip the title matched by name only
  await dialog.getByRole('row',{name:/Kaguya-sama/}).getByRole('button',{name:'Change'}).click();
  await dialog.getByRole('combobox',{name:'Series on this server'}).selectOption('0');
  await expect(dialog.getByRole('row',{name:/Kaguya-sama/}).getByText('Not imported')).toBeVisible();

  await dialog.getByRole('checkbox',{name:/Followed titles/}).uncheck();
  await dialog.getByRole('button',{name:'Import 1 titles'}).click();
  await expect(page.getByText('Your data was imported')).toBeVisible();
  expect(sent.body.options).toMatchObject({progress:true,history:true,follows:false,settings:true,picked:{'1':0}});
  expect(sent.body.data.user).toBe('anna');
});

test('says so when the file is not an export',async({page})=>{
  await mockAccount(page);
  await page.goto('/account');
  const card=page.locator('section',{has:page.getByRole('heading',{name:'Your data',exact:true})});
  await card.locator('input[type=file]').first().setInputFiles({name:'x.json',mimeType:'application/json',buffer:Buffer.from('{"hello":1}')});
  await expect(card.getByText("This isn't a mangarr data export.")).toBeVisible();
});
