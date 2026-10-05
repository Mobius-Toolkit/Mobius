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

test('a click on a link of the app opens its page with no page load', async ({
  page,
}) => {
  const main = page.getByRole('main')
  const link = (path: string) =>
    page.locator(`a[href="${path}"]`).filter({ visible: true })
  const loaded = () => page.evaluate('window.loaded === true')

  await page.goto('/workstreams/plants/garden/16')
  await expect(main.getByText('Note 12 of #16.')).toBeVisible()
  await page.evaluate('window.loaded = true')

  // The side bar of a desktop.
  await link('/inbox').click()
  await expect(page).toHaveURL('/inbox')
  await expect(main.getByText('Inbox', { exact: true })).toBeVisible()
  await link('/workstreams/plants/garden/16').click()
  await expect(page).toHaveURL('/workstreams/plants/garden/16')
  await expect(main.getByText('Note 12 of #16.')).toBeVisible()

  // The tabs and the Settings links of a phone.
  await page.setViewportSize({ width: 390, height: 844 })
  await link('/settings').click()
  await expect(page).toHaveURL('/settings')
  await link('/settings/checkup').click()
  await expect(page).toHaveURL('/settings/checkup')
  await expect(main.getByText('Checkup', { exact: true })).toBeVisible()
  await link('/workstreams').click()
  await expect(page).toHaveURL('/workstreams')
  await link('/workstreams/plants/garden/17').click()
  await expect(page).toHaveURL('/workstreams/plants/garden/17')
  await expect(main.getByText('Note 12 of #17.')).toBeVisible()
  expect(await loaded()).toBe(true)

  await page.goBack()
  await expect(page).toHaveURL('/workstreams')
  await page.goBack()
  await expect(page).toHaveURL('/settings/checkup')
  await expect(main.getByText('Checkup', { exact: true })).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL('/settings')
  expect(await loaded()).toBe(true)
})

test('a link marks only its own page as the current page', async ({ page }) => {
  await page.goto('/workstreams/new')
  const current = page.locator('[aria-current="page"]')
  await expect(current.filter({ visible: true })).toHaveText([
    'New Workstream',
  ])
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(current.filter({ visible: true })).toHaveText(['Workstreams'])
  await page.goto('/settings/checkup')
  await expect(current.filter({ visible: true })).toHaveCount(0)
})

test('a click on another chat shows no state of the previous chat', async ({
  page,
}) => {
  const main = page.getByRole('main')
  const input = page.getByLabel('Message to the Lead')

  await page.goto('/workstreams/plants/garden/16')
  await expect(main.getByText('Note 12 of #16.')).toBeVisible()
  await input.fill('A draft for #16.')
  await page.locator('nav a[href="/workstreams/plants/garden/17"]').click()
  await expect(page).toHaveURL('/workstreams/plants/garden/17')
  await expect(main.getByText('Note 12 of #17.')).toBeVisible()
  await expect(main.getByText('Note 12 of #16.')).toHaveCount(0)
  await expect(input).toHaveValue('')
})

test('a new page starts at the top', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 200 })
  await page.goto('/workstreams')
  await expect(page.getByRole('main').getByText('Workstreams')).toBeVisible()
  await page.evaluate('window.scrollTo(0, document.body.scrollHeight)')
  expect(await page.evaluate('window.scrollY')).toBeGreaterThan(0)
  await page.getByRole('link', { name: 'Inbox' }).filter({ visible: true }).click()
  await expect(page).toHaveURL('/inbox')
  expect(await page.evaluate('window.scrollY')).toBe(0)
})
