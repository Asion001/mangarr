import { test, expect, type Page } from '@playwright/test';
async function mock(page:Page, permissions=['admin']){
  await page.route('**/api/v1/**',async route=>{
    const p=new URL(route.request().url()).pathname;
    let body:unknown={};
    if(p.endsWith('/auth/status')) body={authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions}};
    else if(p.endsWith('/queue'))body={total:0,state:{paused:false},items:[]};
    else if(p.endsWith('/reading/shelf'))body={items:[]};
    else if(p.endsWith('/health'))body={checks:[]};
    else if(p.endsWith('/series/search'))body={items:[],page:1,pageSize:36,total:0,totalSize:0,languages:[]};
    else if(p.endsWith('/series')||p.endsWith('/tags')||p.endsWith('/rootfolders')||p.endsWith('/read/continue')||p.endsWith('/modules'))body=[];
    else if(p.endsWith('/settings/general')) {await route.fulfill({status:503,json:{detail:'Test settings unavailable'}});return;}
    await route.fulfill({json:body});
  });
}
test('reading is the default; editing and collapsed navigation persist',async({page})=>{
  await mock(page);
  await page.goto('/account');
  const mode=page.getByRole('group',{name:'Mode'});
  await expect(mode.getByRole('button',{name:'Read',exact:true})).toHaveAttribute('aria-pressed','true');
  await expect(page.getByRole('link',{name:'Settings',exact:true})).toHaveCount(0);
  await mode.getByRole('button',{name:'Manage',exact:true}).click();
  await expect(mode.getByRole('button',{name:'Manage',exact:true})).toHaveAttribute('aria-pressed','true');
  await expect(page.getByRole('link',{name:'Settings',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Collapse navigation'}).click();
  await expect(page.getByTestId('desktop-navigation')).toHaveCSS('width','64px');
  await page.reload();
  await expect(page.getByRole('button',{name:'Switch to reading mode'})).toBeVisible();
  await expect(page.getByTestId('desktop-navigation')).toHaveCSS('width','64px');
});
test('a permitted management link enables editing',async({page})=>{
  await mock(page);
  await page.goto('/settings/general');
  await expect(page.getByText('Switched to Manage mode')).toBeVisible();
  const mode=page.getByRole('group',{name:'Mode'});
  await expect(mode.getByRole('button',{name:'Manage',exact:true})).toHaveAttribute('aria-pressed','true');
  await mode.getByRole('button',{name:'Read',exact:true}).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('heading',{name:'Series',exact:true})).toBeVisible();
  await expect(page.getByRole('link',{name:'Settings',exact:true})).toHaveCount(0);
});
test('reader permissions never expose editing',async({page})=>{
  await mock(page,[]);
  await page.goto('/settings/general');
  await expect(page.getByText('Not available for your account')).toBeVisible();
  await expect(page.getByRole('button',{name:/Switch to .* mode/})).toHaveCount(0);
  await expect(page.getByRole('group',{name:'Mode'})).toHaveCount(0);
});
test('mobile expanded menu scrolls with account visible, traps focus and closes with Escape',async({page})=>{
  await page.setViewportSize({width:390,height:600});
  await mock(page);
  await page.goto('/settings/general');
  const opener=page.getByRole('button',{name:'Open navigation'});
  await opener.click();
  const dialog=page.getByRole('dialog',{name:'Navigation'});
  await expect(dialog).toBeVisible();
  const nav=dialog.getByRole('navigation');
  expect(await nav.evaluate(el=>el.scrollHeight>el.clientHeight)).toBe(true);
  const account=dialog.getByRole('link',{name:'My account'});
  await expect(account).toBeInViewport();
  await nav.evaluate(el=>el.scrollTop=el.scrollHeight);
  await expect(dialog.getByRole('link',{name:'System',exact:true})).toBeInViewport();
  await expect(account).toBeInViewport();
  await dialog.getByRole('button',{name:'Close navigation'}).focus();
  await page.keyboard.press('Shift+Tab');
  await expect(dialog.getByRole('button',{name:'Log out'})).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
});
test('settings on a phone use the same tab row as system',async({page})=>{
  await mock(page);
  await page.setViewportSize({width:390,height:700});
  await page.goto('/settings/general');
  const tabs=page.getByRole('navigation',{name:'Settings'}).filter({has:page.getByRole('link',{name:'Media management'})}).last();
  await expect(tabs.getByRole('link',{name:'General',exact:true})).toHaveAttribute('aria-current','page');
  await expect(tabs.getByRole('link',{name:'General',exact:true})).toBeInViewport();
  await expect(page.getByRole('combobox')).toHaveCount(0);
  await tabs.getByRole('link',{name:'Users & groups'}).click();
  await expect(page).toHaveURL(/\/settings\/users$/);
});
