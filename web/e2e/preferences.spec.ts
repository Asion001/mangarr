import { test, expect } from '@playwright/test';
test('switches languages and keeps an anonymous preference after reload',async({page})=>{
  await page.route('**/api/v1/**',async route=>{
    const p=new URL(route.request().url()).pathname;
    const body=p.endsWith('/auth/status')?{authenticated:true,authDisabled:true,account:{kind:'anonymous',id:0,permissions:[]}}:{};
    await route.fulfill({json:body});
  });
  await page.goto('/account');
  await page.getByRole('combobox').first().selectOption('ru');
  await expect(page.locator('html')).toHaveAttribute('lang','ru');
  await expect(page.getByRole('heading',{name:'Мой аккаунт'})).toBeVisible();
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('lang','ru');
  await page.getByRole('combobox').first().selectOption('uk');
  await expect(page.locator('html')).toHaveAttribute('lang','uk');
  await expect(page.getByRole('heading',{name:'Мій обліковий запис'})).toBeVisible();
});

test('saves signed-in editor mode and language without response-only fields',async({page})=>{
  const saves:unknown[]=[];
  await page.route('**/api/v1/**',async route=>{
    const request=route.request();
    const p=new URL(request.url()).pathname;
    if(p.endsWith('/auth/status')) {
      await route.fulfill({json:{authenticated:true,account:{kind:'user',id:1,permissions:['admin']}}});
      return;
    }
    if(p.endsWith('/me/ui-preferences')) {
      if(request.method()==='PUT') {
        const body=request.postDataJSON();
        saves.push(body);
        await route.fulfill({json:{...body,updatedAt:'2026-09-17T00:00:00Z'}});
      } else {
        await route.fulfill({json:{locale:'auto',mode:'reading',updatedAt:'2026-09-17T00:00:00Z'}});
      }
      return;
    }
    if(p.endsWith('/me/notifications')||p.endsWith('/me/notifications/schema')||p.endsWith('/me/library-accounts')||p.endsWith('/me/sessions')) {
      await route.fulfill({json:[]});
      return;
    }
    await route.fulfill({json:{}});
  });
  await page.goto('/account');
  await page.getByRole('group',{name:'Mode'}).getByRole('button',{name:'Manage',exact:true}).click();
  await page.getByRole('combobox').first().selectOption('ru');
  await expect.poll(()=>saves).toEqual([
    {locale:'auto',mode:'editing',options:{}},
    {locale:'ru',mode:'editing',options:{}},
  ]);
});
