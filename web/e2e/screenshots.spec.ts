import { expect, test, type Locator, type Page } from '@playwright/test'

const viewports = {
  desktop: { width: 1280, height: 800 },
  phone: { width: 390, height: 844 },
}

// Writes screenshots/<name>-desktop.png and screenshots/<name>-phone.png.
async function screenshot(
  page: Page,
  name: string,
  path: string,
  ready: () => Locator,
  open?: () => Promise<void>,
) {
  for (const [device, size] of Object.entries(viewports)) {
    await page.setViewportSize(size)
    await page.goto(path)
    await open?.()
    await expect(ready()).toBeVisible()
    await page.screenshot({
      path: `screenshots/${name}-${device}.png`,
      animations: 'disabled',
    })
  }
}

test('screenshots', async ({ page }) => {
  const main = page.getByRole('main')

  await screenshot(page, 'login', '/github', () =>
    page.getByLabel('Access password'),
  )
  await page.getByLabel('Access password').fill('correct horse')
  await page.getByRole('button', { name: 'Log in' }).click()
  await screenshot(page, 'github-connect', '/github', () =>
    page.getByLabel('App name'),
  )

  for (const [account, slug] of [
    ['owner', 'mobius-test'],
    ['plants', 'mobius-second'],
  ]) {
    await page.getByLabel('Account or organization').fill(account)
    await page.getByLabel('App name').fill(`Mobius ${account}`)
    await page.getByRole('button', { name: 'Create the App' }).click()
    await expect(
      page.getByText(`Install ${slug} on your repositories`),
    ).toBeVisible()
  }
  // The organization switch shows when the poll has the repositories of both Apps.
  await expect(async () => {
    await page.reload()
    await expect(page.getByRole('button', { name: 'owner' })).toBeVisible({
      timeout: 1000,
    })
  }).toPass()

  await page.goto('/')
  await expect(page).toHaveURL('/settings')
  await screenshot(page, 'settings', '/settings', () =>
    main.getByRole('link', { name: 'Devices' }),
  )
  await screenshot(
    page,
    'organizations',
    '/settings',
    () => page.getByRole('menuitemradio', { name: 'plants' }),
    () => page.getByRole('button', { name: 'owner' }).click(),
  )
  await screenshot(page, 'github', '/github', () =>
    main.getByText('plants/garden'),
  )
  await screenshot(page, 'devices', '/devices', () =>
    main.getByText('This device'),
  )
  await screenshot(page, 'checkup', '/settings/checkup', () =>
    main.getByText('wrong color: #ededed'),
  )
})
