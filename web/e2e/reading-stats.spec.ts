import { test, expect, type Page } from '@playwright/test';

const populated={
  totalActiveSeconds:3*3600+25*60,
  completedChapters:42,
  activeMonth:{month:'2026-08',activeSeconds:2*3600},
  topSeries:{seriesId:7,title:'Sample Series',activeSeconds:90*60,completedChapters:30},
  languages:[{language:'en',activeSeconds:3*3600,completedChapters:35},{language:'und',activeSeconds:25*60,completedChapters:7}],
  genres:[{genre:'Action',activeSeconds:3*3600,completedChapters:40},{genre:'Romance',activeSeconds:0,completedChapters:2}],
};
const empty={totalActiveSeconds:0,completedChapters:0,languages:[],genres:[]};

/** mockAccount serves a signed-in account page; stats answers /me/reading-stats. */
async function mockAccount(page:Page,stats:(route:import('@playwright/test').Route)=>Promise<void>) {
  let preferences:Record<string,unknown>={locale:'en',mode:'reading'};
  await page.route('**/api/v1/**',async route=>{
    const p=new URL(route.request().url()).pathname;
    if(p.endsWith('/auth/status')) return route.fulfill({json:{authenticated:true,account:{kind:'user',id:1,username:'reader',permissions:[]}}});
    if(p.endsWith('/me/reading-stats')) return stats(route);
    if(p.endsWith('/me/ui-preferences')) {
      // keep what was saved: a new locale refetches every query, preferences included
      if(route.request().method()==='PUT') preferences=route.request().postDataJSON();
      return route.fulfill({json:{...preferences,updatedAt:'2026-09-17T00:00:00Z'}});
    }
    if(p.endsWith('/me/notifications')||p.endsWith('/me/notifications/schema')||p.endsWith('/me/library-accounts')||p.endsWith('/me/sessions')) return route.fulfill({json:[]});
    return route.fulfill({json:{}});
  });
}

const card=(page:Page,name='Reading statistics')=>page.locator('section',{has:page.getByRole('heading',{name,exact:true})});

test('shows a reader their reading time, top series and breakdowns',async({page})=>{
  await mockAccount(page,route=>route.fulfill({json:populated}));
  await page.goto('/account');
  const stats=card(page);
  await expect(stats.getByText('3 hr 25 min',{exact:true})).toBeVisible();
  await expect(stats.getByText('42',{exact:true})).toBeVisible();
  await expect(stats.getByText('August 2026')).toBeVisible();
  await expect(stats.getByRole('link',{name:'Sample Series'})).toHaveAttribute('href','/series/7');
  await expect(stats.getByText('1 hr 30 min · 30 chapters')).toBeVisible();
  const languages=stats.getByRole('region',{name:'Languages'});
  await expect(languages.getByText('English')).toBeVisible();
  await expect(languages.getByText('Unknown language')).toBeVisible();
  const genres=stats.getByRole('region',{name:'Genres'});
  await expect(genres.getByText('Action')).toBeVisible();
  await expect(genres.getByText('2 chapters',{exact:true})).toBeVisible();
  // the rest of the page still renders beside it
  await expect(page.getByRole('heading',{name:"Where you're signed in"})).toBeVisible();
});

test('formats the statistics in Ukrainian',async({page})=>{
  await mockAccount(page,route=>route.fulfill({json:populated}));
  await page.goto('/account');
  await page.getByRole('combobox').first().selectOption('uk');
  const stats=card(page,'Статистика читання');
  await expect(stats.getByText('3 год 25 хв',{exact:true})).toBeVisible();
  await expect(stats.getByText('Найактивніший місяць')).toBeVisible();
  await expect(stats.getByText('1 год 30 хв · 30 розділів')).toBeVisible();
  await expect(stats.getByRole('region',{name:'Мови'}).getByText('Англійська')).toBeVisible();
});

test('explains an empty profile',async({page})=>{
  await mockAccount(page,route=>route.fulfill({json:empty}));
  await page.goto('/account');
  const stats=card(page);
  await expect(stats.getByText('Nothing read yet.',{exact:false})).toBeVisible();
  await expect(stats.getByRole('region')).toHaveCount(0);
});

test('shows loading, then an error that can be retried without blocking the page',async({page})=>{
  let calls=0;
  let release=()=>{};
  const held=new Promise<void>(resolve=>{release=resolve;});
  await mockAccount(page,async route=>{
    // the first request and its two automatic retries fail
    calls++;
    if(calls<=3) {
      if(calls===1) await held;
      return route.fulfill({status:500,json:{title:'Internal Server Error',detail:'database is locked',status:500}});
    }
    return route.fulfill({json:populated});
  });
  await page.goto('/account');
  const stats=card(page);
  await expect(stats.getByRole('status',{name:'Loading reading statistics'})).toBeVisible();
  await expect(page.getByRole('heading',{name:"Where you're signed in"})).toBeVisible();
  release();
  await expect(stats.getByText('Could not load reading statistics')).toBeVisible({timeout:15000});
  await stats.getByRole('button',{name:'Try again'}).click();
  await expect(stats.getByText('3 hr 25 min',{exact:true})).toBeVisible();
});
