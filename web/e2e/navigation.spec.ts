import { expect, test } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  await page.goto('/github')
  await page.getByLabel('Access password').fill('correct horse')
  await page.getByRole('button', { name: 'Log in' }).click()
  await expect(page.getByLabel('Access password')).toBeHidden()
})

test('back and forward move between the pages', async ({ page }) => {
  const main = page.getByRole('main')
  const link = (path: string) =>
    page.locator(`a[href="${path}"]`).filter({ visible: true })

  await page.goto('/workstreams')
  await expect(main.getByText('Integrate loyalty plans')).toBeVisible()
  await link('/inbox').click()
  await expect(page).toHaveURL('/inbox')
  await expect(main.getByText('Inbox', { exact: true })).toBeVisible()
  await link('/activity').click()
  await expect(page).toHaveURL('/activity')
  await expect(main.getByText('Activity', { exact: true })).toBeVisible()

  await page.goBack()
  await expect(page).toHaveURL('/inbox')
  await expect(main.getByText('Inbox', { exact: true })).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL('/workstreams')
  await expect(main.getByText('Integrate loyalty plans')).toBeVisible()
  await page.goForward()
  await expect(page).toHaveURL('/inbox')
  await expect(main.getByText('Inbox', { exact: true })).toBeVisible()
})

test('an unknown path goes to the Workstreams', async ({ page }) => {
  const main = page.getByRole('main')

  for (const path of [
    '/nowhere',
    '/workstreams/owner/shop/abc',
    '/workstreams/owner',
  ]) {
    await page.goto(path)
    await expect(page).toHaveURL('/workstreams')
    await expect(main.getByText('Integrate loyalty plans')).toBeVisible()
  }
})
