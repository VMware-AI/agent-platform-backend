import { chromium } from 'playwright';

async function main() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });

  const page = await context.newPage();

  // Collect console messages
  const consoleErrors = [];
  page.on('console', msg => {
    if (msg.type() === 'error' || msg.type() === 'warning') {
      consoleErrors.push(`[${msg.type()}] ${msg.text()}`);
    }
  });
  page.on('pageerror', err => {
    consoleErrors.push(`[PAGE_ERROR] ${err.message}`);
  });

  // Collect network responses for /agents query
  let agentsResponseBody = null;
  page.on('response', async response => {
    const url = response.url();
    if (url.includes('/agents') || url.includes('/graphql')) {
      try {
        const body = await response.text();
        if (body && body.length < 50000 && body.includes('status')) {
          agentsResponseBody = body;
          console.log('[NETWORK] Captured response from:', url);
        }
      } catch {}
    }
  });

  try {
    // Step 1: Navigate to login
    console.log('=== Step 1: Navigate to app ===');
    await page.goto('http://192.168.15.128:5173', { waitUntil: 'networkidle', timeout: 30000 });
    await page.waitForTimeout(1000);
    await page.screenshot({ path: '/tmp/e2e-screenshots/debug-login.png', fullPage: false });

    // Login
    console.log('=== Step 2: Login ===');
    // Try different login form selectors
    const emailInput = await page.$('input[type="email"], input[name="email"], input[placeholder*="email" i], input[placeholder*="邮箱"]');
    const passwordInput = await page.$('input[type="password"], input[name="password"]');

    if (emailInput && passwordInput) {
      await emailInput.fill('admin@platform.local');
      await passwordInput.fill('ChangeMe123!');

      // Find and click submit button
      const submitBtn = await page.$('button[type="submit"], button:has-text("登录"), button:has-text("Sign in")');
      if (submitBtn) {
        await submitBtn.click();
        await page.waitForTimeout(3000);
      }
    } else {
      console.log('Login form not found with standard selectors, checking page content...');
      const bodyText = await page.textContent('body');
      console.log('Body preview:', bodyText.substring(0, 500));
    }

    // Step 2: Navigate to /agents/list
    console.log('\n=== Step 3: Navigate to /agents/list ===');
    await page.goto('http://192.168.15.128:5173/agents/list', { waitUntil: 'networkidle', timeout: 30000 });

    // Step 3: Wait for data to load
    console.log('Waiting 5 seconds for data load...');
    await page.waitForTimeout(5000);

    // Step 4: Screenshot
    await page.screenshot({ path: '/tmp/e2e-screenshots/debug-status.png', fullPage: true });
    console.log('Screenshot saved to /tmp/e2e-screenshots/debug-status.png');

    // Step 5: Inspect DOM for status badges
    console.log('\n=== Step 5: Check status badges in DOM ===');

    const statusInfo = await page.evaluate(() => {
      // Try multiple selector patterns for status badges
      const results = [];

      // Pattern 1: .status-badge
      document.querySelectorAll('.status-badge, [class*="status"], [class*="badge"]').forEach(el => {
        results.push({
          selector: 'status-badge/status class',
          text: el.textContent?.trim(),
          class: el.className,
          html: el.outerHTML.substring(0, 200),
        });
      });

      // Pattern 2: Look for table/card rows and check status cells
      const rows = document.querySelectorAll('tr, [class*="row"], [class*="card"], [class*="agent-item"]');
      rows.forEach(row => {
        const statusEl = row.querySelector('[class*="status"], [class*="badge"], td:nth-child(3), td:nth-child(4)');
        if (statusEl) {
          const text = statusEl.textContent?.trim();
          if (text && text.length < 50) {
            results.push({
              selector: 'row-status',
              rowClass: row.className,
              statusText: text,
              statusClass: statusEl.className,
            });
          }
        }
      });

      // Pattern 3: Just dump all text to find anything status-related
      const allStatusTexts = [];
      const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, null, false);
      let node;
      while (node = walker.nextNode()) {
        const t = node.textContent?.trim();
        if (t && (t.includes('provisioning') || t.includes('Provisioning') ||
                  t.includes('部署') || t.includes('运行') || t.includes('停止') ||
                  t.includes('online') || t.includes('offline') || t.includes('pending') ||
                  t.includes('active') || t.includes('inactive') || t.includes('error'))) {
          allStatusTexts.push({ text: t, parentClass: node.parentElement?.className });
        }
      }

      return { specific: results, textWalker: allStatusTexts };
    });

    console.log('Status info found:', JSON.stringify(statusInfo, null, 2));

    // Step 5b: dump agent table structure
    console.log('\n=== Dumping page structure around agent list ===');
    const pageStructure = await page.evaluate(() => {
      // Find common containers
      const containers = [];
      document.querySelectorAll('[class*="agent"], [class*="table"], [class*="list"], [class*="grid"], main, section, article').forEach(el => {
        containers.push({
          tag: el.tagName,
          class: el.className,
          id: el.id,
          childCount: el.children.length,
          textPreview: el.textContent?.trim().substring(0, 150),
        });
      });
      return containers;
    });
    console.log('Page structure:', JSON.stringify(pageStructure, null, 2));

    // Step 6: Check network response
    console.log('\n=== Step 6: Network response for agents query ===');
    if (agentsResponseBody) {
      console.log('Agents response:', agentsResponseBody.substring(0, 2000));
    } else {
      console.log('No agents response captured via listener. Checking via performance API...');
      const netLogs = await page.evaluate(() => {
        const entries = performance.getEntriesByType('resource');
        return entries
          .filter(e => e.name.includes('graphql') || e.name.includes('api') || e.name.includes('agent'))
          .map(e => ({ url: e.name, duration: e.duration }));
      });
      console.log('API requests found:', JSON.stringify(netLogs, null, 2));
    }

    // Step 7: Console errors
    console.log('\n=== Step 7: Console errors ===');
    if (consoleErrors.length > 0) {
      console.log('Console errors/warnings:');
      consoleErrors.forEach((e, i) => console.log(`  ${i + 1}. ${e}`));
    } else {
      console.log('No console errors or warnings captured.');
    }

    // Extra: Take more screenshots of different states
    await page.screenshot({ path: '/tmp/e2e-screenshots/debug-status-fullpage.png', fullPage: true });

  } catch (err) {
    console.error('ERROR:', err.message);
    try {
      await page.screenshot({ path: '/tmp/e2e-screenshots/debug-error.png' });
      console.log('Error screenshot saved');
    } catch {}
  }

  await browser.close();
  console.log('\n=== Done ===');
}

main().catch(console.error);
